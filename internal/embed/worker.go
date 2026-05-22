package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
)

const (
	QueueName        = "embed:queue"
	DLQName          = "embed:queue:dlq"
	DefaultWorkers   = 2
	DefaultBatchMax  = 32
	DefaultBatchWait = 100 * time.Millisecond
	DefaultBLPopWait = 5 * time.Second
	CircuitOpenPause = 30 * time.Second
	MaxAttempts      = 5
	DefaultModel     = "voyage-code-3"
)

// Embedder is the worker-facing surface of *Router.
type Embedder interface {
	Embed(context.Context, EmbedRequest) (EmbedResponse, error)
}

// EmbedderFunc adapts a function into an Embedder for tests.
type EmbedderFunc func(context.Context, EmbedRequest) (EmbedResponse, error)

func (f EmbedderFunc) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	return f(ctx, req)
}

type circuitOpenChecker interface {
	CircuitOpen() bool
}

// RedisList is the Redis list subset used by the worker. *redis.Client
// satisfies it directly; tests use a small in-memory fake.
type RedisList interface {
	BLPop(ctx context.Context, timeout time.Duration, keys ...string) *goredis.StringSliceCmd
	RPush(ctx context.Context, key string, values ...interface{}) *goredis.IntCmd
}

// Store persists one session embedding after a provider response.
type Store interface {
	UpsertEmbedding(ctx context.Context, job Job, vec []float32, model string) error
}

// StoreFunc adapts a function into a Store for tests.
type StoreFunc func(context.Context, Job, []float32, string) error

func (f StoreFunc) UpsertEmbedding(ctx context.Context, job Job, vec []float32, model string) error {
	return f(ctx, job, vec, model)
}

type pgStore struct {
	db *pgxpool.Pool
}

func (s pgStore) UpsertEmbedding(ctx context.Context, job Job, vec []float32, model string) error {
	return db.WithTenant(ctx, s.db, job.TenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.UpsertEmbedding(ctx, tx, job.SessionID, job.TenantID, vec, model)
		return err
	})
}

type Sleeper func(context.Context, time.Duration) error

type WorkerConfig struct {
	DB        *pgxpool.Pool
	Redis     RedisList
	Embedder  Embedder
	Store     Store
	Logger    *slog.Logger
	Count     int
	Model     string
	BatchMax  int
	BatchWait time.Duration
	BLPopWait time.Duration
	Sleep     Sleeper
}

type Worker struct {
	redis     RedisList
	embedder  Embedder
	store     Store
	logger    *slog.Logger
	count     int
	model     string
	batchMax  int
	batchWait time.Duration
	blpopWait time.Duration
	sleep     Sleeper
}

func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.Redis == nil {
		return nil, errors.New("embed worker: redis is required")
	}
	if cfg.Embedder == nil {
		return nil, errors.New("embed worker: embedder is required")
	}
	store := cfg.Store
	if store == nil {
		if cfg.DB == nil {
			return nil, errors.New("embed worker: db or store is required")
		}
		store = pgStore{db: cfg.DB}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	count := cfg.Count
	if count <= 0 {
		count = DefaultWorkers
	}
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	batchMax := cfg.BatchMax
	if batchMax <= 0 {
		batchMax = DefaultBatchMax
	}
	batchWait := cfg.BatchWait
	if batchWait <= 0 {
		batchWait = DefaultBatchWait
	}
	blpopWait := cfg.BLPopWait
	if blpopWait <= 0 {
		blpopWait = DefaultBLPopWait
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return &Worker{
		redis:     cfg.Redis,
		embedder:  cfg.Embedder,
		store:     store,
		logger:    logger,
		count:     count,
		model:     model,
		batchMax:  batchMax,
		batchWait: batchWait,
		blpopWait: blpopWait,
		sleep:     sleep,
	}, nil
}

func CountFromEnv() int {
	n, err := strconv.Atoi(os.Getenv("EMBED_WORKER_COUNT"))
	if err != nil || n <= 0 {
		return DefaultWorkers
	}
	return n
}

func (w *Worker) Start(ctx context.Context) {
	for i := 0; i < w.count; i++ {
		go w.Run(ctx)
	}
}

func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.ProcessOneBatch(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("embed worker batch failed", "err", err)
		}
	}
}

func (w *Worker) ProcessOneBatch(ctx context.Context) error {
	if w.circuitOpen() {
		w.logger.Warn("embed provider circuit open; pausing worker")
		return w.sleep(ctx, CircuitOpenPause)
	}

	first, err := w.pop(ctx, w.blpopWait)
	if err != nil {
		if errors.Is(err, goredis.Nil) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	batch := []Job{first}
	deadline := time.Now().Add(w.batchWait)
	for len(batch) < w.batchMax {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		job, err := w.pop(ctx, remaining)
		if err != nil {
			if errors.Is(err, goredis.Nil) || errors.Is(err, context.Canceled) {
				break
			}
			return err
		}
		batch = append(batch, job)
	}
	return w.handleBatch(ctx, batch)
}

func (w *Worker) pop(ctx context.Context, timeout time.Duration) (Job, error) {
	res, err := w.redis.BLPop(ctx, timeout, QueueName).Result()
	if err != nil {
		return Job{}, err
	}
	if len(res) != 2 {
		return Job{}, fmt.Errorf("embed worker: BLPOP returned %d values", len(res))
	}
	var job Job
	if err := json.Unmarshal([]byte(res[1]), &job); err != nil {
		return Job{}, err
	}
	if job.SourceText == "" {
		return Job{}, errors.New("embed worker: source_text required")
	}
	if job.TenantID == uuid.Nil || job.SessionID == uuid.Nil {
		return Job{}, errors.New("embed worker: tenant_id and session_id required")
	}
	return job, nil
}

func (w *Worker) handleBatch(ctx context.Context, batch []Job) error {
	inputs := make([]string, len(batch))
	for i, job := range batch {
		inputs[i] = job.SourceText
	}
	resp, err := w.embedder.Embed(ctx, EmbedRequest{Model: w.model, Inputs: inputs})
	if err != nil {
		if errors.Is(err, ErrRateLimited) {
			w.logger.Warn("embed_rate_limited", "batch_size", len(batch))
			if sleepErr := w.sleep(ctx, backoffForAttempt(1)); sleepErr != nil {
				return sleepErr
			}
			return w.requeueBatch(ctx, batch)
		}
		if errors.Is(err, ErrAllProvidersUnavailable) && w.circuitOpen() {
			if requeueErr := w.requeueBatch(ctx, batch); requeueErr != nil {
				return requeueErr
			}
			return w.sleep(ctx, CircuitOpenPause)
		}
		for _, job := range batch {
			if retryErr := w.retryOrDLQ(ctx, job, err); retryErr != nil {
				return retryErr
			}
		}
		return nil
	}
	if len(resp.Vectors) != len(batch) {
		err := fmt.Errorf("embed worker: provider returned %d vectors for %d inputs", len(resp.Vectors), len(batch))
		for _, job := range batch {
			if retryErr := w.retryOrDLQ(ctx, job, err); retryErr != nil {
				return retryErr
			}
		}
		return nil
	}
	for i, job := range batch {
		if err := w.store.UpsertEmbedding(ctx, job, resp.Vectors[i], w.model); err != nil {
			if retryErr := w.retryOrDLQ(ctx, job, err); retryErr != nil {
				return retryErr
			}
		}
	}
	return nil
}

func (w *Worker) circuitOpen() bool {
	checker, ok := w.embedder.(circuitOpenChecker)
	return ok && checker.CircuitOpen()
}

func (w *Worker) requeueBatch(ctx context.Context, batch []Job) error {
	for _, job := range batch {
		if err := w.pushJob(ctx, QueueName, job); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) retryOrDLQ(ctx context.Context, job Job, cause error) error {
	nextAttempt := job.Attempts + 1
	if nextAttempt >= MaxAttempts {
		return w.pushDLQ(ctx, job, cause)
	}
	job.Attempts = nextAttempt
	if err := w.sleep(ctx, backoffForAttempt(nextAttempt)); err != nil {
		return err
	}
	return w.pushJob(ctx, QueueName, job)
}

func (w *Worker) pushDLQ(ctx context.Context, job Job, cause error) error {
	body, err := json.Marshal(dlqEntry{
		Job:        job,
		FinalError: cause.Error(),
		FailedAt:   time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return w.redis.RPush(ctx, DLQName, string(body)).Err()
}

func (w *Worker) pushJob(ctx context.Context, queue string, job Job) error {
	body, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return w.redis.RPush(ctx, queue, string(body)).Err()
}

func backoffForAttempt(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Second
	}
	if attempt > MaxAttempts {
		attempt = MaxAttempts
	}
	return time.Second << (attempt - 1)
}

// Job is the JSON payload consumed from embed:queue.
type Job struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	SessionID  uuid.UUID `json:"session_id"`
	SourceText string    `json:"source_text"`
	QueuedAt   time.Time `json:"queued_at,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
}

func (j *Job) UnmarshalJSON(data []byte) error {
	type rawJob struct {
		TenantID       uuid.UUID `json:"tenant_id"`
		SessionID      uuid.UUID `json:"session_id"`
		SourceText     string    `json:"source_text"`
		RedactedPrompt string    `json:"redacted_prompt"`
		QueuedAt       time.Time `json:"queued_at"`
		Attempts       int       `json:"attempts"`
	}
	var raw rawJob
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	j.TenantID = raw.TenantID
	j.SessionID = raw.SessionID
	j.SourceText = raw.SourceText
	if j.SourceText == "" {
		j.SourceText = raw.RedactedPrompt
	}
	j.QueuedAt = raw.QueuedAt
	j.Attempts = raw.Attempts
	return nil
}

type dlqEntry struct {
	Job        Job       `json:"job"`
	FinalError string    `json:"error"`
	FailedAt   time.Time `json:"failed_at"`
}
