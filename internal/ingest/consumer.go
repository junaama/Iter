package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/internal/redact"
	iredis "github.com/iter-dev/iter/internal/redis"
)

var zeroUUID uuid.UUID

type Classifier func([]byte) (redact.Classification, []byte, error)

type WorkerConfig struct {
	DB         *pgxpool.Pool
	Redis      *goredis.Client
	Logger     *slog.Logger
	Classifier Classifier
	Now        func() time.Time
	Count      int
	Streams    []string
}

type Worker struct {
	db       *pgxpool.Pool
	redis    *goredis.Client
	logger   *slog.Logger
	classify Classifier
	now      func() time.Time
	count    int
	streams  []string
	consumer string
	mu       sync.Mutex
	started  map[string]struct{}
}

func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.DB == nil {
		return nil, errors.New("ingest worker: db is required")
	}
	if cfg.Redis == nil {
		return nil, errors.New("ingest worker: redis is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	classify := cfg.Classifier
	if classify == nil {
		classify = redact.Classify
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	count := cfg.Count
	if count <= 0 {
		count = DefaultWorkers
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return &Worker{
		db:       cfg.DB,
		redis:    cfg.Redis,
		logger:   logger,
		classify: classify,
		now:      now,
		count:    count,
		streams:  append([]string(nil), cfg.Streams...),
		consumer: fmt.Sprintf("%s-%d", host, os.Getpid()),
		started:  map[string]struct{}{},
	}, nil
}

func (w *Worker) ConsumerName() string {
	return w.consumer
}

func (w *Worker) Start(ctx context.Context) error {
	if len(w.streams) > 0 {
		return w.startStreams(ctx, w.streams)
	}
	if err := w.discoverAndStart(ctx); err != nil {
		return err
	}
	go w.discoveryLoop(ctx)
	return nil
}

func (w *Worker) discoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(streamDiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.discoverAndStart(ctx); err != nil {
				w.logger.Warn("ingest stream discovery failed", "err", err)
			}
		}
	}
}

func (w *Worker) discoverAndStart(ctx context.Context) error {
	streams, err := w.discoverStreams(ctx)
	if err != nil {
		return err
	}
	if len(streams) == 0 {
		w.logger.Warn("ingest worker waiting: no tenant streams discovered")
		return nil
	}
	return w.startStreams(ctx, streams)
}

func (w *Worker) startStreams(ctx context.Context, streams []string) error {
	for _, stream := range streams {
		w.mu.Lock()
		if _, ok := w.started[stream]; ok {
			w.mu.Unlock()
			continue
		}
		w.mu.Unlock()
		if err := iredis.EnsureStreamAndGroup(ctx, w.redis, stream, ConsumerGroup); err != nil {
			return err
		}
		w.mu.Lock()
		if _, ok := w.started[stream]; ok {
			w.mu.Unlock()
			continue
		}
		w.started[stream] = struct{}{}
		if !containsStream(w.streams, stream) {
			w.streams = append(w.streams, stream)
		}
		w.mu.Unlock()
		for i := 0; i < w.count; i++ {
			stream := stream
			go w.loop(ctx, stream)
		}
		w.logger.Info("ingest worker stream started", "stream", stream, "workers", w.count)
	}
	return nil
}

func containsStream(streams []string, stream string) bool {
	for _, candidate := range streams {
		if candidate == stream {
			return true
		}
	}
	return false
}

func (w *Worker) discoverStreams(ctx context.Context) ([]string, error) {
	rows, err := w.db.Query(ctx, `SELECT id FROM tenants WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("ingest discover streams: %w", err)
	}
	defer rows.Close()
	var streams []string
	for rows.Next() {
		var tenantID uuid.UUID
		if err := rows.Scan(&tenantID); err != nil {
			return nil, fmt.Errorf("ingest discover streams scan: %w", err)
		}
		streams = append(streams, StreamName(tenantID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ingest discover streams iter: %w", err)
	}
	return streams, nil
}

func (w *Worker) loop(ctx context.Context, stream string) {
	for ctx.Err() == nil {
		if err := w.reclaim(ctx, stream); err != nil {
			w.logger.Warn("ingest reclaim failed", "stream", stream, "err", err)
		}
		msgs, err := iredis.ReadGroup(ctx, w.redis, stream, ConsumerGroup, w.consumer, readBatchSize, readBlock)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.logger.Warn("ingest read failed", "stream", stream, "err", err)
			continue
		}
		for _, msg := range msgs {
			if err := w.HandleMessage(ctx, msg); err != nil {
				w.logger.Warn("ingest handle failed", "stream", msg.Stream, "id", msg.ID, "err", err)
			}
		}
	}
}

func (w *Worker) reclaim(ctx context.Context, stream string) error {
	res, start, err := w.redis.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
		Stream:   stream,
		Group:    ConsumerGroup,
		Consumer: w.consumer,
		MinIdle:  claimMinIdle,
		Start:    "0-0",
		Count:    readBatchSize,
	}).Result()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return err
	}
	_ = start
	for _, raw := range res {
		values := make(map[string]any, len(raw.Values))
		for k, v := range raw.Values {
			values[k] = v
		}
		if err := w.HandleMessage(ctx, iredis.Msg{Stream: stream, ID: raw.ID, Values: values}); err != nil {
			w.logger.Warn("ingest reclaimed handle failed", "stream", stream, "id", raw.ID, "err", err)
		}
	}
	return nil
}

func (w *Worker) HandleMessage(ctx context.Context, msg iredis.Msg) error {
	q, err := decodeQueuedEvent(msg.Values)
	if err != nil {
		return w.fail(ctx, msg, err)
	}

	classification, redactedPayload, err := w.classify(q.Payload)
	if err != nil {
		w.logger.Warn("ingest classify error", "stream", msg.Stream, "id", msg.ID, "err", err)
	}
	if classification == redact.Dirty {
		if err := w.auditLeak(ctx, q); err != nil {
			return w.fail(ctx, msg, err)
		}
		return iredis.Ack(ctx, w.redis, msg.Stream, ConsumerGroup, msg.ID)
	}

	persisted, prompt, err := w.persist(ctx, q, classification.String(), redactedPayload)
	if err != nil {
		return w.fail(ctx, msg, err)
	}
	if persisted {
		if err := w.enqueueEmbedding(ctx, q.TenantID, q.SessionID, prompt); err != nil {
			w.logger.Warn("ingest embedding enqueue failed after persist",
				"tenant_id", q.TenantID.String(),
				"session_id", q.SessionID.String(),
				"err", err)
		}
	}
	return iredis.Ack(ctx, w.redis, msg.Stream, ConsumerGroup, msg.ID)
}

func (w *Worker) persist(ctx context.Context, q QueuedEvent, classification string, payload []byte) (bool, string, error) {
	var projected sessionProjection
	var payloadMap map[string]any
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &payloadMap); err != nil {
			return false, "", fmt.Errorf("ingest persist: payload json: %w", err)
		}
	}
	projected = projectSession(q, payloadMap)

	eventType, err := parseEventType(q.EventType)
	if err != nil {
		return false, "", err
	}
	insertedEvent := false
	err = db.WithTenant(ctx, w.db, q.TenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if _, _, err := repo.UpsertSession(ctx, tx, repo.Session{
			ID:              q.SessionID,
			TenantID:        q.TenantID,
			UserID:          q.UserID,
			ParentSessionID: projected.ParentID,
			Harness:         projected.Harness,
			Model:           projected.Model,
			Effort:          projected.Effort,
			Tools:           projected.Tools,
			RepoHash:        projected.RepoHash,
			GitBranch:       projected.GitBranch,
			StartedAt:       projected.StartedAt,
			EndedAt:         projected.EndedAt,
			RedactedPrompt:  projected.RedactedPrompt,
			RedactedSystem:  projected.RedactedSystem,
			Classification:  classification,
		}); err != nil {
			return err
		}
		_, inserted, err := repo.UpsertSessionEvent(ctx, tx, repo.SessionEventRow{
			EventID:    &q.EventID,
			SessionID:  q.SessionID,
			TenantID:   q.TenantID,
			EventType:  eventType,
			Payload:    payloadMap,
			OccurredAt: q.OccurredAt,
		})
		if err != nil {
			return err
		}
		insertedEvent = inserted
		return nil
	})
	if err != nil {
		return false, "", err
	}
	return insertedEvent, projected.RedactedPrompt, nil
}

func (w *Worker) auditLeak(ctx context.Context, q QueuedEvent) error {
	details, _ := json.Marshal(map[string]any{
		"msg_id":     q.MsgID.String(),
		"session_id": q.SessionID.String(),
		"event_id":   q.EventID.String(),
		"event_type": q.EventType,
	})
	targetKind := "session"
	targetID := q.SessionID.String()
	return db.WithTenant(ctx, w.db, q.TenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.InsertAuditLog(ctx, tx, repo.AuditLog{
			TenantID:   q.TenantID,
			ActorKind:  repo.ActorKindSystem,
			EventType:  repo.AuditEventLeakDetectedPostIngestion,
			TargetKind: &targetKind,
			TargetID:   &targetID,
			Details:    details,
		})
		return err
	})
}

func (w *Worker) enqueueEmbedding(ctx context.Context, tenantID, sessionID uuid.UUID, prompt string) error {
	body, err := json.Marshal(EmbedJob{
		TenantID:       tenantID,
		SessionID:      sessionID,
		RedactedPrompt: prompt,
		QueuedAt:       w.now().UTC(),
	})
	if err != nil {
		return err
	}
	return w.redis.RPush(ctx, EmbedQueue, string(body)).Err()
}

func (w *Worker) fail(ctx context.Context, msg iredis.Msg, cause error) error {
	retries := readRetries(msg.Values) + 1
	if retries >= MaxRetries {
		values := copyValues(msg.Values)
		values["error"] = cause.Error()
		values["stack"] = string(debug.Stack())
		if err := w.redis.XAdd(ctx, &goredis.XAddArgs{
			Stream: DLQNameFromStream(msg.Stream),
			Values: values,
		}).Err(); err != nil {
			return fmt.Errorf("ingest dlq: %w", err)
		}
		return iredis.Ack(ctx, w.redis, msg.Stream, ConsumerGroup, msg.ID)
	}

	values := copyValues(msg.Values)
	values[RetriesField] = retries
	if err := w.redis.XAdd(ctx, &goredis.XAddArgs{Stream: msg.Stream, Values: values}).Err(); err != nil {
		return fmt.Errorf("ingest retry xadd: %w", err)
	}
	if err := iredis.Ack(ctx, w.redis, msg.Stream, ConsumerGroup, msg.ID); err != nil {
		return err
	}
	return cause
}

func decodeQueuedEvent(values map[string]any) (QueuedEvent, error) {
	raw, ok := values[MessageField]
	if !ok {
		return QueuedEvent{}, errors.New("ingest decode: missing message")
	}
	var body []byte
	switch v := raw.(type) {
	case string:
		body = []byte(v)
	case []byte:
		body = v
	default:
		return QueuedEvent{}, fmt.Errorf("ingest decode: message has type %T", raw)
	}
	var q QueuedEvent
	if err := json.Unmarshal(body, &q); err != nil {
		return QueuedEvent{}, err
	}
	if q.TenantID == zeroUUID || q.UserID == zeroUUID || q.SessionID == zeroUUID || q.EventID == zeroUUID {
		return QueuedEvent{}, errors.New("ingest decode: missing ids")
	}
	return q, nil
}

func projectSession(q QueuedEvent, payload map[string]any) sessionProjection {
	p := sessionProjection{
		Harness:        stringField(payload, "harness", defaultHarness),
		Model:          stringField(payload, "model", defaultModel),
		Tools:          stringSliceField(payload, "tools"),
		RedactedPrompt: firstStringField(payload, defaultPrompt, "redacted_prompt", "prompt"),
		StartedAt:      q.OccurredAt,
	}
	if p.StartedAt.IsZero() {
		p.StartedAt = q.ReceivedAt
	}
	if p.StartedAt.IsZero() {
		p.StartedAt = time.Now().UTC()
	}
	p.Effort = optionalString(payload, "effort")
	p.RepoHash = optionalString(payload, "repo_hash")
	p.GitBranch = optionalString(payload, "git_branch")
	p.RedactedSystem = optionalString(payload, "redacted_system")
	if parent := optionalString(payload, "parent_session_id"); parent != nil {
		if parsed, err := uuid.Parse(*parent); err == nil {
			p.ParentID = &parsed
		}
	}
	if ended := optionalString(payload, "ended_at"); ended != nil {
		if parsed, err := time.Parse(time.RFC3339, *ended); err == nil {
			p.EndedAt = &parsed
		}
	}
	return p
}

func stringField(m map[string]any, key, fallback string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func firstStringField(m map[string]any, fallback string, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	return fallback
}

func optionalString(m map[string]any, key string) *string {
	if v, ok := m[key].(string); ok && v != "" {
		return &v
	}
	return nil
}

func stringSliceField(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func copyValues(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func readRetries(values map[string]any) int {
	switch v := values[RetriesField].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}
