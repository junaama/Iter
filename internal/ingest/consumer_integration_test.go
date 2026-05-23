//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/redact"
	iredis "github.com/iter-dev/iter/internal/redis"
	"github.com/iter-dev/iter/internal/ws"
	"github.com/iter-dev/iter/pkg/contracts"
)

func startRedis(ctx context.Context, t *testing.T) (*goredis.Client, func()) {
	t.Helper()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	uri, err := container.ConnectionString(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("redis ConnectionString: %v", err)
	}
	cfg, err := iredis.ConfigFromURL(uri)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("ConfigFromURL: %v", err)
	}
	client, err := iredis.NewClient(ctx, cfg)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("NewClient: %v", err)
	}
	return client, func() {
		_ = client.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate redis container: %v", err)
		}
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWSHandlerXAddsBeforeAck(t *testing.T) {
	ctx := context.Background()
	redisClient, cleanup := startRedis(ctx, t)
	defer cleanup()

	tenantID := uuid.New()
	userID := uuid.New()
	msgID := uuid.New()
	sessionID := uuid.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	handler := NewWSHandler(redisClient, quietLogger(), func() time.Time { return now })
	payload := map[string]any{"prompt": "ship the thing", "harness": "codex", "model": "gpt-5"}
	payloadJSON, _ := json.Marshal(payload)
	raw, _ := json.Marshal(ws.Ingest{
		Envelope:   ws.Envelope{Type: ws.MessageTypeIngest, MsgID: msgID, SentAt: now},
		SessionID:  sessionID,
		EventType:  string(contracts.EventPromptSent),
		OccurredAt: now,
		Payload:    payloadJSON,
	})

	ack := handler(ctx, contracts.Principal{TenantID: tenantID, UserID: userID}, ws.Envelope{Type: ws.MessageTypeIngest, MsgID: msgID, SentAt: now}, raw)
	if ack.Status != "ok" {
		t.Fatalf("ack status = %s code=%s", ack.Status, ack.Code)
	}
	if n := redisClient.XLen(ctx, StreamName(tenantID)).Val(); n != 1 {
		t.Fatalf("stream length = %d, want 1", n)
	}
	groups, err := redisClient.XInfoGroups(ctx, StreamName(tenantID)).Result()
	if err != nil {
		t.Fatalf("XInfoGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != ConsumerGroup {
		t.Fatalf("groups = %#v, want %s", groups, ConsumerGroup)
	}
}

func TestWorkerPersistsDedupsAndEnqueuesEmbedding(t *testing.T) {
	ctx := context.Background()
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	redisClient, cleanup := startRedis(ctx, t)
	defer cleanup()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "acme"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "adam@example.com", "Adam"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), "member")

	worker, err := NewWorker(WorkerConfig{
		DB:      tdb.AppPool,
		Redis:   redisClient,
		Logger:  quietLogger(),
		Streams: []string{StreamName(tenantID)},
		Classifier: func(b []byte) (redact.Classification, []byte, error) {
			return redact.Clean, b, nil
		},
		Now: func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := iredis.EnsureStreamAndGroup(ctx, redisClient, StreamName(tenantID), ConsumerGroup); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	q := queuedFixture(tenantID, userID)
	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(q)
		id, err := redisClient.XAdd(ctx, &goredis.XAddArgs{
			Stream: StreamName(tenantID),
			Values: map[string]any{MessageField: string(body), RetriesField: 0},
		}).Result()
		if err != nil {
			t.Fatalf("XADD: %v", err)
		}
		if err := worker.HandleMessage(ctx, iredis.Msg{
			Stream: StreamName(tenantID),
			ID:     id,
			Values: map[string]any{MessageField: string(body), RetriesField: "0"},
		}); err != nil {
			t.Fatalf("HandleMessage #%d: %v", i+1, err)
		}
	}

	var sessions, events int
	if err := tdb.Super.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, q.SessionID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := tdb.Super.QueryRowContext(ctx, `SELECT count(*) FROM session_events WHERE session_id = $1`, q.SessionID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if sessions != 1 || events != 1 {
		t.Fatalf("sessions/events = %d/%d, want 1/1", sessions, events)
	}
	if got := redisClient.LLen(ctx, EmbedQueue).Val(); got != 1 {
		t.Fatalf("embed queue length = %d, want 1", got)
	}
}

func TestWorkerDirtyPayloadDropsAndAudits(t *testing.T) {
	ctx := context.Background()
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	redisClient, cleanup := startRedis(ctx, t)
	defer cleanup()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "acme"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "eve@example.com", "Eve"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), "member")
	secret := mustReadSecretFixture(t)
	payload, _ := json.Marshal(map[string]any{"prompt": string(secret), "harness": "codex", "model": "gpt-5"})

	worker, err := NewWorker(WorkerConfig{
		DB:     tdb.AppPool,
		Redis:  redisClient,
		Logger: quietLogger(),
		Classifier: func(b []byte) (redact.Classification, []byte, error) {
			if len(b) == 0 {
				t.Fatal("classifier received empty payload")
			}
			return redact.Dirty, b, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	q := queuedFixture(tenantID, userID)
	q.Payload = payload
	body, _ := json.Marshal(q)
	if err := iredis.EnsureStreamAndGroup(ctx, redisClient, StreamName(tenantID), ConsumerGroup); err != nil {
		t.Fatalf("ensure group: %v", err)
	}
	id, _ := redisClient.XAdd(ctx, &goredis.XAddArgs{
		Stream: StreamName(tenantID),
		Values: map[string]any{MessageField: string(body), RetriesField: 0},
	}).Result()
	if err := worker.HandleMessage(ctx, iredis.Msg{Stream: StreamName(tenantID), ID: id, Values: map[string]any{MessageField: string(body)}}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var sessions, audits int
	if err := tdb.Super.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, q.SessionID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := tdb.Super.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE event_type = 'leak_detected_post_ingestion'`).Scan(&audits); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	if sessions != 0 || audits != 1 {
		t.Fatalf("sessions/audits = %d/%d, want 0/1", sessions, audits)
	}
}

func TestFailMovesToTenantDLQAfterFiveFailures(t *testing.T) {
	ctx := context.Background()
	redisClient, cleanup := startRedis(ctx, t)
	defer cleanup()

	tenantID := uuid.New()
	stream := StreamName(tenantID)
	if err := iredis.EnsureStreamAndGroup(ctx, redisClient, stream, ConsumerGroup); err != nil {
		t.Fatalf("ensure group: %v", err)
	}
	w := &Worker{redis: redisClient, consumer: "test-consumer"}
	id, _ := redisClient.XAdd(ctx, &goredis.XAddArgs{
		Stream: stream,
		Values: map[string]any{MessageField: "{not-json", RetriesField: 4},
	}).Result()
	if err := w.HandleMessage(ctx, iredis.Msg{
		Stream: stream,
		ID:     id,
		Values: map[string]any{MessageField: "{not-json", RetriesField: "4"},
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	entries, err := redisClient.XRange(ctx, DLQName(tenantID), "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE dlq: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("DLQ entries = %d, want 1", len(entries))
	}
	if entries[0].Values["error"] == "" || entries[0].Values["stack"] == "" {
		t.Fatalf("DLQ missing error/stack: %+v", entries[0].Values)
	}
}

func queuedFixture(tenantID, userID uuid.UUID) QueuedEvent {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(map[string]any{
		"prompt":     "implement a clean endpoint",
		"harness":    "codex",
		"model":      "gpt-5",
		"tools":      []string{"shell"},
		"repo_hash":  "abc123",
		"git_branch": "main",
	})
	return QueuedEvent{
		TenantID:   tenantID,
		UserID:     userID,
		MsgID:      uuid.New(),
		SessionID:  uuid.New(),
		EventID:    uuid.New(),
		EventType:  string(contracts.EventPromptSent),
		OccurredAt: now,
		Payload:    payload,
		ReceivedAt: now,
	}
}

func mustReadSecretFixture(t *testing.T) []byte {
	t.Helper()
	for _, rel := range []string{
		"../redact/testdata/secrets/github_pat.txt",
		filepath.Join("..", "redact", "testdata", "secrets", "github_pat.txt"),
	} {
		b, err := os.ReadFile(rel)
		if err == nil {
			return b
		}
	}
	t.Fatal("read secret fixture")
	return nil
}
