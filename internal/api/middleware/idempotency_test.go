package middleware_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/api/middleware"
	"github.com/iter-dev/iter/pkg/contracts"
)

// newRedis spins up an in-process miniredis + go-redis client and
// registers cleanup. Pulled out so every test reads top-down without
// repetitive setup.
func newRedis(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

// withPrincipal returns a request with a synthetic authenticated
// Principal on context, so tests don't have to spin up the auth
// middleware to exercise this one.
func withPrincipal(t *testing.T, r *http.Request, tenantID uuid.UUID) *http.Request {
	t.Helper()
	p := contracts.Principal{
		UserID:   uuid.New(),
		TenantID: tenantID,
	}
	return r.WithContext(contracts.WithPrincipal(r.Context(), p))
}

// countingHandler returns a handler + an atomic counter so tests can
// assert how many times the handler ran.
func countingHandler(body string, status int) (http.Handler, *atomic.Int64) {
	var calls atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "v")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return h, &calls
}

func TestIdempotency_GETBypasses(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := countingHandler(`{"ok":1}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls: got %d want 1", calls.Load())
	}
	if rec.Header().Get("X-Idempotent-Replay") != "" {
		t.Fatalf("GET must never set X-Idempotent-Replay")
	}
}

func TestIdempotency_POSTWithoutKey400(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	req := withPrincipal(t, httptest.NewRequest(http.MethodPost, "/v1/suggest", nil), tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_idempotency_key") {
		t.Fatalf("body: got %q", rec.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("handler must not run; calls=%d", calls.Load())
	}
}

func TestIdempotency_CacheHitReplays(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, calls := countingHandler(`{"answer":42}`, http.StatusCreated)
	mw := middleware.Idempotency(rdb, nil)(h)

	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
		req.Header.Set("Idempotency-Key", "k-1")
		req = withPrincipal(t, req, tenant)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	first := doReq()
	if first.Code != http.StatusCreated {
		t.Fatalf("first status: %d", first.Code)
	}
	if first.Header().Get("X-Idempotent-Replay") == "true" {
		t.Fatalf("first call must NOT carry replay marker")
	}

	second := doReq()
	if second.Code != http.StatusCreated {
		t.Fatalf("second status: %d", second.Code)
	}
	if second.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("second call must carry X-Idempotent-Replay: true")
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("body mismatch: %q vs %q", second.Body, first.Body)
	}
	if second.Header().Get("X-Custom") != "v" {
		t.Fatalf("custom header not preserved on replay")
	}
	if calls.Load() != 1 {
		t.Fatalf("handler must run exactly once; got %d", calls.Load())
	}
}

func TestIdempotency_CrossTenantSafe(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	for _, tenant := range []uuid.UUID{uuid.New(), uuid.New()} {
		req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
		req.Header.Set("Idempotency-Key", "shared-key")
		req = withPrincipal(t, req, tenant)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("tenant %s: status %d", tenant, rec.Code)
		}
		if rec.Header().Get("X-Idempotent-Replay") == "true" {
			t.Fatalf("tenant %s must not see replay (key shared, tenant not)", tenant)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("two tenants should hit handler twice; got %d", calls.Load())
	}
}

func TestIdempotency_ConcurrentSingleFlight(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	// Slow handler so the lock-holder is observably in-flight while
	// the other goroutines try to acquire.
	var calls atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(80 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"v":1}`))
	})
	mw := middleware.Idempotency(rdb, nil,
		middleware.WithPollEvery(5*time.Millisecond),
		middleware.WithWaitTimeout(5*time.Second),
	)(h)

	const N = 100
	var wg sync.WaitGroup
	bodies := make([]string, N)
	replays := make([]string, N)

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
			req.Header.Set("Idempotency-Key", "single-flight")
			req = withPrincipal(t, req, tenant)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			bodies[i] = rec.Body.String()
			replays[i] = rec.Header().Get("X-Idempotent-Replay")
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("handler must run exactly once; got %d", got)
	}
	// Every caller saw the same body.
	for i := 1; i < N; i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("response divergence at %d", i)
		}
	}
	// At least one replay must be flagged. (The lock-holder doesn't
	// flag itself; everyone else does.)
	flagged := 0
	for _, r := range replays {
		if r == "true" {
			flagged++
		}
	}
	if flagged < N-1 {
		t.Fatalf("expected ≥%d replays; got %d", N-1, flagged)
	}
}

func TestIdempotency_LockTimeoutAllowsTakeover(t *testing.T) {
	t.Parallel()
	mr, rdb := newRedis(t)
	tenant := uuid.New()

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil,
		middleware.WithLockTTL(200*time.Millisecond),
		middleware.WithWaitTimeout(50*time.Millisecond),
		middleware.WithPollEvery(5*time.Millisecond),
	)(h)

	// Simulate a crashed prior request: prepopulate the lock key
	// directly. No cache key, so the second request must take over
	// once the lock expires.
	lockKey := "idempotency:lock:" + tenant.String() + ":POST:/v1/suggest:k-2"
	_ = rdb.Set(context.Background(), lockKey, "1", 200*time.Millisecond).Err()

	// First retry: lock still held, wait times out, falls through.
	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "k-2")
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first attempt: %d", rec.Code)
	}

	// Advance miniredis past the lock TTL so the holder is gone.
	mr.FastForward(300 * time.Millisecond)

	// Second retry: should successfully acquire the lock.
	calls.Store(0)
	req = httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "k-2")
	req = withPrincipal(t, req, tenant)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second attempt: %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("after TTL the handler must run; calls=%d", calls.Load())
	}
}

func TestIdempotency_BodyTooLargeBypassesCache(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	big := strings.Repeat("x", 2048)
	h, calls := countingHandler(big, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil,
		middleware.WithMaxBodyBytes(1024),
	)(h)

	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
		req.Header.Set("Idempotency-Key", "k-big")
		req = withPrincipal(t, req, tenant)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	first := doReq()
	second := doReq()

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("status: %d / %d", first.Code, second.Code)
	}
	if calls.Load() != 2 {
		t.Fatalf("oversize bodies must always re-run handler; calls=%d", calls.Load())
	}
	if second.Header().Get("X-Idempotent-Replay") == "true" {
		t.Fatalf("oversize body must never set replay marker")
	}
}

func TestIdempotency_NilRedisFailOpen(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(nil, nil)(h)

	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
		req.Header.Set("Idempotency-Key", "k-nil")
		req = withPrincipal(t, req, tenant)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 3; i++ {
		rec := doReq()
		if rec.Code != http.StatusOK {
			t.Fatalf("status: %d", rec.Code)
		}
		if rec.Header().Get("X-Idempotent-Replay") == "true" {
			t.Fatalf("nil redis must never produce replays")
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("nil redis must always run handler; calls=%d", calls.Load())
	}
}

func TestIdempotency_WebhookPathSkipped(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	doReq := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		// No Idempotency-Key — proves the skip happens before key
		// enforcement.
		req = withPrincipal(t, req, tenant)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	for _, p := range []string{"/v1/webhooks/github", "/v1/webhooks/linear"} {
		rec := doReq(p)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status: %d", p, rec.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("webhooks should pass through to handler; calls=%d", calls.Load())
	}

	// A non-webhook POST without key still 400s — proves the skip
	// list is path-scoped, not global.
	rec := doReq("/v1/suggest")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-webhook must still 400; got %d", rec.Code)
	}
}

func TestIdempotency_MissingPrincipal500(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "k-no-principal")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler must not run without principal; calls=%d", calls.Load())
	}
}

func TestIdempotency_CustomSkipPrefix(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, _ := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil, middleware.WithSkipPrefix("/internal/"))(h)

	req := httptest.NewRequest(http.MethodPost, "/internal/probe", nil)
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip prefix should bypass; got %d", rec.Code)
	}
}

// TestIdempotency_OptionsApplied wires every option through to confirm
// they reach the config. The behavior matrix is already covered by the
// targeted tests above; this one just guards against a regression where
// an option silently no-ops.
func TestIdempotency_OptionsApplied(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, _ := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil,
		middleware.WithCacheTTL(time.Hour),
		middleware.WithLockTTL(5*time.Second),
		middleware.WithWaitTimeout(100*time.Millisecond),
		middleware.WithPollEvery(10*time.Millisecond),
		middleware.WithMaxBodyBytes(4096),
	)(h)

	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "opts")
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
}

// TestIdempotency_BodyIsBytePreserved ensures non-UTF8 / arbitrary
// bytes survive the base64 round-trip. JSON is the common case; this
// covers the corner where a handler returns msgpack / protobuf.
func TestIdempotency_BodyIsBytePreserved(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	payload := []byte{0x00, 0xff, 0x10, 0x7f, 0x80, 0x81}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	mw := middleware.Idempotency(rdb, nil)(h)

	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
		req.Header.Set("Idempotency-Key", "bin")
		req = withPrincipal(t, req, tenant)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	_ = doReq()
	second := doReq()

	got, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("binary round-trip mismatch: %x vs %x", got, payload)
	}
	if second.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("replay marker missing on binary cache hit")
	}
}

// TestIdempotency_CorruptCacheEntryFallsThrough verifies that a
// malformed Redis value (left over from a bad deploy, partial write,
// etc.) doesn't 500 — the middleware logs and re-runs the handler.
func TestIdempotency_CorruptCacheEntryFallsThrough(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	// Pre-seed a malformed cache entry the middleware will Get.
	cacheKey := "idempotency:" + tenant.String() + ":POST:/v1/suggest:bad"
	_ = rdb.Set(context.Background(), cacheKey, "{not json", time.Minute).Err()

	h, calls := countingHandler(`{"ok":1}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "bad")
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("corrupt cache must fall through; calls=%d", calls.Load())
	}
}

// TestIdempotency_HealthSkipped — explicit defense-in-depth check that
// /health bypasses even if it ever became a POST.
func TestIdempotency_HealthSkipped(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("/health should bypass; calls=%d", calls.Load())
	}
}

// TestIdempotency_DifferentPathsCacheSeparately confirms the cache
// key is per-route: two POSTs with the same tenant + key but different
// URL paths must not collide.
func TestIdempotency_DifferentPathsCacheSeparately(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	doReq := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Idempotency-Key", "k-shared")
		req = withPrincipal(t, req, tenant)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	_ = doReq("/v1/suggest")
	second := doReq("/v1/sessions")
	if second.Header().Get("X-Idempotent-Replay") == "true" {
		t.Fatalf("different paths must not share cache")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls: %d want 2", calls.Load())
	}
}

// TestIdempotency_ChiRouteContextNoPanic verifies the middleware
// composes cleanly through a chi router (route resolution happens
// after middleware in chi, but the request must still go through).
func TestIdempotency_ChiRouteContextNoPanic(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, _ := countingHandler(`{}`, http.StatusOK)
	r := chi.NewRouter()
	r.Use(middleware.Idempotency(rdb, nil))
	r.Post("/v1/suggest", h.ServeHTTP)

	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "chi-k")
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
}

// TestIdempotency_RedisErrorOnLockFallsThrough simulates a Redis hard
// failure during SETNX by closing miniredis underneath the client. The
// middleware must fall open (run the handler) rather than 500.
func TestIdempotency_RedisErrorOnLockFallsThrough(t *testing.T) {
	t.Parallel()
	mr, rdb := newRedis(t)
	tenant := uuid.New()

	h, calls := countingHandler(`{}`, http.StatusOK)
	mw := middleware.Idempotency(rdb, nil)(h)

	mr.Close() // every subsequent Redis call now errors

	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "err")
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("redis-error path must fail open; got %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler must run on redis error; calls=%d", calls.Load())
	}
}

// TestIdempotency_CachedResponseStructure pokes Redis directly to
// confirm the persisted JSON matches the documented shape — a
// regression catch in case the schema changes accidentally.
func TestIdempotency_CachedResponseStructure(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)
	tenant := uuid.New()

	h, _ := countingHandler(`{"hello":"world"}`, http.StatusAccepted)
	mw := middleware.Idempotency(rdb, nil)(h)

	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", nil)
	req.Header.Set("Idempotency-Key", "shape")
	req = withPrincipal(t, req, tenant)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rec.Code)
	}

	raw, err := rdb.Get(context.Background(),
		"idempotency:"+tenant.String()+":POST:/v1/suggest:shape").Bytes()
	if err != nil {
		t.Fatalf("redis get: %v", err)
	}
	var got struct {
		Status  int                 `json:"status"`
		Headers map[string][]string `json:"headers"`
		BodyB64 string              `json:"body_b64"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != http.StatusAccepted {
		t.Fatalf("status: %d", got.Status)
	}
	if got.BodyB64 == "" {
		t.Fatalf("body_b64 empty")
	}
	if _, ok := got.Headers["Content-Type"]; !ok {
		t.Fatalf("Content-Type missing from cached headers: %v", got.Headers)
	}
}
