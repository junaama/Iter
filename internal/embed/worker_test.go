package embed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type workerStubProvider struct {
	mu       sync.Mutex
	calls    []EmbedRequest
	failWith error
}

func (p *workerStubProvider) Name() string { return "stub" }

func (p *workerStubProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, EmbedRequest{
		Model:  req.Model,
		Inputs: append([]string(nil), req.Inputs...),
	})
	failWith := p.failWith
	p.mu.Unlock()
	if failWith != nil {
		return EmbedResponse{}, failWith
	}
	vectors := make([][]float32, len(req.Inputs))
	for i := range req.Inputs {
		vectors[i] = testVector(float32(i + 1))
	}
	return EmbedResponse{Vectors: vectors, TokensIn: len(req.Inputs)}, nil
}

func (p *workerStubProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *workerStubProvider) firstBatchSize() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return 0
	}
	return len(p.calls[0].Inputs)
}

type fakeWorkerRedis struct {
	mu     sync.Mutex
	queue  []string
	dlq    []string
	closed bool
}

func newFakeWorkerRedis() *fakeWorkerRedis { return &fakeWorkerRedis{} }

func (r *fakeWorkerRedis) BLPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd {
	r.mu.Lock()
	if len(r.queue) > 0 {
		msg := r.queue[0]
		r.queue = r.queue[1:]
		r.mu.Unlock()
		return redis.NewStringSliceResult([]string{keys[0], msg}, nil)
	}
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return redis.NewStringSliceResult(nil, ctx.Err())
	default:
		return redis.NewStringSliceResult(nil, redis.Nil)
	}
}

func (r *fakeWorkerRedis) RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		switch key {
		case QueueName:
			r.queue = append(r.queue, v.(string))
		case DLQName:
			r.dlq = append(r.dlq, v.(string))
		}
	}
	return redis.NewIntResult(int64(len(r.queue)), nil)
}

func (r *fakeWorkerRedis) queueLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queue)
}

func (r *fakeWorkerRedis) dlqLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.dlq)
}

func (r *fakeWorkerRedis) dlqEntry(t *testing.T) dlqEntry {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.dlq) == 0 {
		t.Fatal("missing dlq entry")
	}
	var out dlqEntry
	if err := json.Unmarshal([]byte(r.dlq[0]), &out); err != nil {
		t.Fatalf("decode dlq: %v", err)
	}
	return out
}

func quietWorkerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testJob(sessionID uuid.UUID, text string) Job {
	return Job{
		TenantID:   uuid.New(),
		SessionID:  sessionID,
		SourceText: text,
		QueuedAt:   time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	}
}

func enqueueFake(t *testing.T, r *fakeWorkerRedis, jobs ...Job) {
	t.Helper()
	for _, job := range jobs {
		body, err := json.Marshal(job)
		if err != nil {
			t.Fatalf("marshal job: %v", err)
		}
		if err := r.RPush(context.Background(), QueueName, string(body)).Err(); err != nil {
			t.Fatalf("RPush: %v", err)
		}
	}
}

func testVector(fill float32) []float32 {
	v := make([]float32, 1536)
	for i := range v {
		v[i] = fill
	}
	return v
}

func TestWorkerBatchesThirtyTwoMessagesInOneProviderCall(t *testing.T) {
	ctx := context.Background()
	rdb := newFakeWorkerRedis()
	provider := &workerStubProvider{}
	router := NewRouter(RouterConfig{
		Providers: []Provider{provider},
		Priority:  []string{"stub"},
	})
	worker, err := NewWorker(WorkerConfig{
		Redis:     rdb,
		Embedder:  router,
		Store:     StoreFunc(func(context.Context, Job, []float32, string) error { return nil }),
		Logger:    quietWorkerLogger(),
		BatchWait: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	for i := 0; i < 32; i++ {
		enqueueFake(t, rdb, testJob(uuid.New(), "prompt"))
	}

	if err := worker.ProcessOneBatch(ctx); err != nil {
		t.Fatalf("ProcessOneBatch: %v", err)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.callCount())
	}
	if provider.firstBatchSize() != 32 {
		t.Fatalf("batch size = %d, want 32", provider.firstBatchSize())
	}
}

func TestWorkerCacheHitAvoidsProviderCall(t *testing.T) {
	ctx := context.Background()
	rdb := newFakeWorkerRedis()
	cacheRedis := newFakeRedis()
	cached := testVector(7)
	cacheRedis.store[cacheKey(DefaultModel, "same prompt")] = mustEncodeVectorForTest(t, cached)
	provider := &workerStubProvider{}
	router := NewRouter(RouterConfig{
		Providers: []Provider{provider},
		Priority:  []string{"stub"},
		Cache:     NewCache(CacheConfig{Redis: cacheRedis}),
	})
	var persisted int
	worker, err := NewWorker(WorkerConfig{
		Redis:    rdb,
		Embedder: router,
		Store: StoreFunc(func(ctx context.Context, job Job, vec []float32, model string) error {
			persisted++
			if vec[0] != cached[0] {
				t.Fatalf("stored vector = %v, want cached", vec[0])
			}
			return nil
		}),
		Logger: quietWorkerLogger(),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	enqueueFake(t, rdb, testJob(uuid.New(), "same prompt"))

	if err := worker.ProcessOneBatch(ctx); err != nil {
		t.Fatalf("ProcessOneBatch: %v", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.callCount())
	}
	if persisted != 1 {
		t.Fatalf("persisted = %d, want 1", persisted)
	}
}

func TestWorkerRetriesWithBackoffThenDLQ(t *testing.T) {
	ctx := context.Background()
	rdb := newFakeWorkerRedis()
	job := testJob(uuid.New(), "retry me")
	enqueueFake(t, rdb, job)
	var sleeps []time.Duration
	worker, err := NewWorker(WorkerConfig{
		Redis: rdb,
		Embedder: EmbedderFunc(func(context.Context, EmbedRequest) (EmbedResponse, error) {
			return EmbedResponse{}, errors.New("provider boom")
		}),
		Store:  StoreFunc(func(context.Context, Job, []float32, string) error { return nil }),
		Logger: quietWorkerLogger(),
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	for i := 0; i < MaxAttempts; i++ {
		if err := worker.ProcessOneBatch(ctx); err != nil {
			t.Fatalf("ProcessOneBatch %d: %v", i+1, err)
		}
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleep[%d] = %s, want %s", i, sleeps[i], want[i])
		}
	}
	if rdb.dlqLen() != 1 {
		t.Fatalf("dlq len = %d, want 1", rdb.dlqLen())
	}
	entry := rdb.dlqEntry(t)
	if entry.Job.SessionID != job.SessionID || entry.FinalError == "" {
		t.Fatalf("bad dlq entry: %+v", entry)
	}
}

func TestWorkerCircuitOpenPausesWithoutDraining(t *testing.T) {
	ctx := context.Background()
	rdb := newFakeWorkerRedis()
	enqueueFake(t, rdb, testJob(uuid.New(), "stays queued"))
	var slept time.Duration
	worker, err := NewWorker(WorkerConfig{
		Redis:    rdb,
		Embedder: circuitOpenEmbedder{},
		Store:    StoreFunc(func(context.Context, Job, []float32, string) error { return nil }),
		Logger:   quietWorkerLogger(),
		Sleep: func(ctx context.Context, d time.Duration) error {
			slept = d
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	if err := worker.ProcessOneBatch(ctx); err != nil {
		t.Fatalf("ProcessOneBatch: %v", err)
	}
	if slept != CircuitOpenPause {
		t.Fatalf("slept = %s, want %s", slept, CircuitOpenPause)
	}
	if rdb.queueLen() != 1 || rdb.dlqLen() != 0 {
		t.Fatalf("queue/dlq len = %d/%d, want 1/0", rdb.queueLen(), rdb.dlqLen())
	}
}

func TestWorkerShutdownFinishesInFlightBatch(t *testing.T) {
	rdb := newFakeWorkerRedis()
	enqueueFake(t, rdb, testJob(uuid.New(), "one"), testJob(uuid.New(), "two"))
	var persisted int
	ctx, cancel := context.WithCancel(context.Background())
	worker, err := NewWorker(WorkerConfig{
		Redis: rdb,
		Embedder: EmbedderFunc(func(context.Context, EmbedRequest) (EmbedResponse, error) {
			cancel()
			return EmbedResponse{Vectors: [][]float32{testVector(1)}}, nil
		}),
		Store: StoreFunc(func(context.Context, Job, []float32, string) error {
			persisted++
			return nil
		}),
		Logger:   quietWorkerLogger(),
		BatchMax: 1,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	worker.Run(ctx)
	if persisted != 1 {
		t.Fatalf("persisted = %d, want 1", persisted)
	}
	if rdb.queueLen() != 1 {
		t.Fatalf("remaining queue = %d, want 1", rdb.queueLen())
	}
}

func mustEncodeVectorForTest(t *testing.T, vec []float32) []byte {
	t.Helper()
	raw, err := encodeVector(vec)
	if err != nil {
		t.Fatalf("encode vector: %v", err)
	}
	return raw
}

type circuitOpenEmbedder struct{}

func (circuitOpenEmbedder) Embed(context.Context, EmbedRequest) (EmbedResponse, error) {
	return EmbedResponse{}, ErrAllProvidersUnavailable
}

func (circuitOpenEmbedder) CircuitOpen() bool { return true }
