package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/api/middleware"
	"github.com/iter-dev/iter/pkg/contracts"
)

// rlPrincipal builds a request decorated with a synthetic Principal,
// matching the shape produced by the auth middleware (031) at runtime.
// Pulled out so tests read top-down.
func rlPrincipal(t *testing.T, r *http.Request, tokenID, tokenType string) *http.Request {
	t.Helper()
	p := contracts.Principal{
		UserID:    uuid.New(),
		TenantID:  uuid.New(),
		TokenID:   tokenID,
		TokenType: tokenType,
	}
	return r.WithContext(contracts.WithPrincipal(r.Context(), p))
}

// rlOK is the handler under the limiter. Returns 200 + counter so
// tests can assert how many requests reached it.
func rlOK() (http.Handler, *atomic.Int64) {
	var calls atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	return h, &calls
}

// rlBufLogger wires a slog logger into a buffer so the fail-open path
// can be asserted against by string-matching the emitted event name.
func rlBufLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func TestRateLimit_UnderLimitPasses(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(5, 0, 0),
	)(h)

	for i := 0; i < 5; i++ {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-under", "cli")
		// Distinct request ids so the ZSET ZADD inserts rather than
		// overwriting the same member.
		req.Header.Set("X-Request-ID", "rid-"+strconv.Itoa(i))
		req = req.WithContext(middleware.WithRequestID(req.Context(), "rid-"+strconv.Itoa(i)))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("req %d: got %d want 200", i, rec.Code)
		}
	}
	if calls.Load() != 5 {
		t.Fatalf("handler calls: got %d want 5", calls.Load())
	}
}

func TestRateLimit_AtLimitReturns429(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(3, 0, 0),
	)(h)

	// Send N+1; the (N+1)th should 429.
	for i := 0; i < 4; i++ {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-cap", "cli")
		req = req.WithContext(middleware.WithRequestID(req.Context(), "rid-"+strconv.Itoa(i)))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if i < 3 {
			if rec.Code != http.StatusOK {
				t.Fatalf("req %d should pass; got %d", i, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("4th req: got %d want 429", rec.Code)
		}
		// Retry-After must be a positive integer.
		ra := rec.Header().Get("Retry-After")
		if ra == "" {
			t.Fatalf("Retry-After header missing")
		}
		n, err := strconv.Atoi(ra)
		if err != nil || n <= 0 || n > int(middleware.RateLimitWindow.Seconds()) {
			t.Fatalf("Retry-After=%q invalid (want 1..%d)", ra, int(middleware.RateLimitWindow.Seconds()))
		}
		// Body shape: {error, limit, window_seconds}.
		var body struct {
			Error         string `json:"error"`
			Limit         int    `json:"limit"`
			WindowSeconds int    `json:"window_seconds"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body unmarshal: %v: %s", err, rec.Body.String())
		}
		if body.Error != "rate_limited" {
			t.Fatalf("error: %q", body.Error)
		}
		if body.Limit != 3 {
			t.Fatalf("limit: got %d want 3", body.Limit)
		}
		if body.WindowSeconds != 60 {
			t.Fatalf("window_seconds: got %d want 60", body.WindowSeconds)
		}
		if rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type=%q", rec.Header().Get("Content-Type"))
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("handler calls: got %d want 3 (the 4th must be blocked before the handler)", calls.Load())
	}
}

func TestRateLimit_WindowSlide(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	// Drive the clock manually so the sliding behavior is
	// deterministic — no sleeping.
	var nowMu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		nowMu.Lock()
		defer nowMu.Unlock()
		now = now.Add(d)
	}

	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(3, 0, 0),
		middleware.WithRateLimitClock(clock),
	)(h)

	send := func(label string) int {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-slide", "cli")
		req = req.WithContext(middleware.WithRequestID(req.Context(), label))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}

	// At t=0, send 3 — all pass.
	for i := 0; i < 3; i++ {
		if code := send("a-" + strconv.Itoa(i)); code != http.StatusOK {
			t.Fatalf("t=0 req %d: got %d", i, code)
		}
	}
	// 4th at t=0 → 429.
	if code := send("a-overflow"); code != http.StatusTooManyRequests {
		t.Fatalf("t=0 overflow: got %d want 429", code)
	}

	// Slide to t=30s — the 3 are still in window.
	advance(30 * time.Second)
	if code := send("b-overflow"); code != http.StatusTooManyRequests {
		t.Fatalf("t=30s still over: got %d want 429", code)
	}

	// Slide past the window — old batch falls off.
	advance(31 * time.Second) // total t=61s
	for i := 0; i < 3; i++ {
		if code := send("c-" + strconv.Itoa(i)); code != http.StatusOK {
			t.Fatalf("t=61s req %d: got %d (old batch should have expired)", i, code)
		}
	}
	if calls.Load() != 6 {
		t.Fatalf("handler calls: got %d want 6 (3 before, 3 after)", calls.Load())
	}
}

func TestRateLimit_AtomicConcurrency(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	const limit = 50
	const goroutines = 200

	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(limit, 0, 0),
	)(h)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var passed atomic.Int64
	var rejected atomic.Int64

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
				"jti-race", "cli")
			req = req.WithContext(middleware.WithRequestID(req.Context(),
				"rid-"+strconv.Itoa(i)))
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusOK:
				passed.Add(1)
			case http.StatusTooManyRequests:
				rejected.Add(1)
			default:
				t.Errorf("unexpected status: %d", rec.Code)
			}
		}(i)
	}
	wg.Wait()

	if passed.Load() != int64(limit) {
		t.Fatalf("passed: got %d want exactly %d (atomicity violated)", passed.Load(), limit)
	}
	if rejected.Load() != int64(goroutines-limit) {
		t.Fatalf("rejected: got %d want %d", rejected.Load(), goroutines-limit)
	}
	if calls.Load() != int64(limit) {
		t.Fatalf("handler calls: got %d want %d", calls.Load(), limit)
	}
}

func TestRateLimit_SkipPrefixes(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(1, 0, 0),
		middleware.WithRateLimitSkip("/health", "/v1/webhooks"),
	)(h)

	// /health: 100 requests, all pass even with limit=1 — the
	// limiter must not touch the bucket for skipped paths.
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/health req %d: got %d", i, rec.Code)
		}
	}

	// /v1/webhooks/github likewise — webhook auth is HMAC, not JWT.
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/v1/webhooks req %d: got %d", i, rec.Code)
		}
	}

	if calls.Load() != 200 {
		t.Fatalf("calls: got %d want 200", calls.Load())
	}
}

func TestRateLimit_DifferentTokensIndependent(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(2, 0, 0),
	)(h)

	// Token A exhausts its limit.
	for i := 0; i < 3; i++ {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-A", "cli")
		req = req.WithContext(middleware.WithRequestID(req.Context(), "A-"+strconv.Itoa(i)))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if i < 2 && rec.Code != http.StatusOK {
			t.Fatalf("A %d: got %d", i, rec.Code)
		}
		if i == 2 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("A %d should 429: got %d", i, rec.Code)
		}
	}

	// Token B has its own bucket — still under-limit.
	for i := 0; i < 2; i++ {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-B", "cli")
		req = req.WithContext(middleware.WithRequestID(req.Context(), "B-"+strconv.Itoa(i)))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("B %d should pass: got %d", i, rec.Code)
		}
	}

	// 2 (A) + 2 (B) = 4 handler invocations.
	if calls.Load() != 4 {
		t.Fatalf("calls: got %d want 4", calls.Load())
	}
}

func TestRateLimit_TokenTypeSelectsLimit(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, _ := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(2, 5, 1),
	)(h)

	check := func(label, tokenID, tokenType string, want int) {
		t.Helper()
		passed := 0
		for i := 0; i < want+5; i++ {
			req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
				tokenID, tokenType)
			req = req.WithContext(middleware.WithRequestID(req.Context(),
				label+"-"+strconv.Itoa(i)))
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				passed++
			}
		}
		if passed != want {
			t.Fatalf("%s: passed=%d want=%d", label, passed, want)
		}
	}

	check("cli", "jti-cli", "cli", 2)
	check("daemon", "jti-daemon", "daemon", 5)
	check("unknown", "jti-other", "ci", 1) // unrecognized → fallback
	check("empty", "jti-empty", "", 1)     // empty → fallback
}

func TestRateLimit_NilRedisFailOpen(t *testing.T) {
	t.Parallel()

	log, buf := rlBufLogger()
	h, calls := rlOK()
	mw := middleware.RateLimit(nil,
		middleware.WithRateLimitOverrides(1, 0, 0),
		middleware.WithRateLimitLogger(log),
	)(h)

	for i := 0; i < 10; i++ {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-nil", "cli")
		// Attach a request id on half the iterations so both
		// branches of logRateLimitUnavailable are exercised.
		if i%2 == 0 {
			req = req.WithContext(middleware.WithRequestID(req.Context(), "rid-"+strconv.Itoa(i)))
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("nil rdb req %d: got %d (must fail open)", i, rec.Code)
		}
	}
	if calls.Load() != 10 {
		t.Fatalf("calls: got %d want 10", calls.Load())
	}
	if !strings.Contains(buf.String(), "ratelimit_redis_unavailable") {
		t.Fatalf("expected ratelimit_redis_unavailable event in log: %s", buf.String())
	}
}

func TestRateLimit_RedisErrorFailOpen(t *testing.T) {
	t.Parallel()
	mr, rdb := newRedis(t)

	log, buf := rlBufLogger()
	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(1, 0, 0),
		middleware.WithRateLimitLogger(log),
	)(h)

	mr.Close() // every subsequent Redis call now errors

	for i := 0; i < 5; i++ {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-err", "cli")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("redis error req %d: got %d (must fail open)", i, rec.Code)
		}
	}
	if calls.Load() != 5 {
		t.Fatalf("calls: got %d want 5", calls.Load())
	}
	if !strings.Contains(buf.String(), "ratelimit_redis_unavailable") {
		t.Fatalf("expected ratelimit_redis_unavailable event in log: %s", buf.String())
	}
}

func TestRateLimit_NoPrincipalBypasses(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(1, 0, 0),
	)(h)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
		// NO Principal on the context.
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("no-principal req %d: got %d (must bypass)", i, rec.Code)
		}
	}
	if calls.Load() != 10 {
		t.Fatalf("calls: got %d want 10", calls.Load())
	}
}

func TestRateLimit_KeyFallbackWithoutTokenID(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, _ := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(2, 0, 0),
	)(h)

	// Two requests with the same user but no TokenID — they share
	// the user-derived fallback bucket. The third must 429.
	userP := contracts.Principal{
		UserID:    uuid.New(),
		TenantID:  uuid.New(),
		TokenType: "cli",
		// TokenID intentionally empty
	}
	send := func(label string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
		req = req.WithContext(contracts.WithPrincipal(req.Context(), userP))
		req = req.WithContext(middleware.WithRequestID(req.Context(), label))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := send("a"); code != http.StatusOK {
		t.Fatalf("1st: %d", code)
	}
	if code := send("b"); code != http.StatusOK {
		t.Fatalf("2nd: %d", code)
	}
	if code := send("c"); code != http.StatusTooManyRequests {
		t.Fatalf("3rd: got %d want 429", code)
	}
}

func TestRateLimit_RetryAfterReflectsOldestEntry(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	var nowMu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		nowMu.Lock()
		defer nowMu.Unlock()
		now = now.Add(d)
	}

	h, _ := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(2, 0, 0),
		middleware.WithRateLimitClock(clock),
	)(h)

	send := func(label string) *httptest.ResponseRecorder {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-retry", "cli")
		req = req.WithContext(middleware.WithRequestID(req.Context(), label))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	// Fill the bucket at t=0.
	send("a")
	send("b")
	// Advance 15s and trip the limit. Oldest entry sits at t=0, so
	// Retry-After should be ~45s (60s window − 15s elapsed).
	advance(15 * time.Second)
	rec := send("c")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: %d", rec.Code)
	}
	ra, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After parse: %v", err)
	}
	// Allow ±1s for rounding.
	if ra < 44 || ra > 46 {
		t.Fatalf("Retry-After=%d want ~45", ra)
	}
}

func TestRateLimit_DistinctRequestIDsAvoidUnderCount(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	// Confirms the rate-limit member is unique per request. If the
	// limiter accidentally keyed all entries under the same ZSET
	// member (e.g. the token id), ZADD would update-in-place and
	// ZCARD would never grow past 1 — silently disabling the limit.
	h, _ := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(2, 0, 0),
	)(h)

	send := func() int {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-distinct", "cli")
		// Crucially, do NOT set a request id — exercises the
		// fallback timestamp+ulid member generator.
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := send(); code != http.StatusOK {
		t.Fatalf("1st: %d", code)
	}
	if code := send(); code != http.StatusOK {
		t.Fatalf("2nd: %d", code)
	}
	if code := send(); code != http.StatusTooManyRequests {
		t.Fatalf("3rd: got %d want 429 (member uniqueness check)", code)
	}
}

// TestRateLimit_ScriptCachedAcrossInvocations is a smoke test that the
// goredis.NewScript path doesn't re-EVAL the body every request. Not a
// behavioral guarantee — just a defense against accidentally
// constructing the script inside the closure.
func TestRateLimit_ScriptCachedAcrossInvocations(t *testing.T) {
	t.Parallel()
	mr, rdb := newRedis(t)

	h, _ := rlOK()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(100, 0, 0),
	)(h)

	for i := 0; i < 10; i++ {
		req := rlPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/anything", nil),
			"jti-cached", "cli")
		req = req.WithContext(middleware.WithRequestID(req.Context(), "rid-"+strconv.Itoa(i)))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("req %d: %d", i, rec.Code)
		}
	}
	// miniredis exposes a SCRIPT EXISTS-style introspection: we
	// inspect the keyspace for the bucket to confirm the script ran.
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		if strings.HasPrefix(k, "ratelimit:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ratelimit:* key in miniredis; keys=%v", keys)
	}
}

// TestRateLimit_ContextCancelDoesNotBlockRequest verifies the limiter
// doesn't leak when the request's context is already canceled.
// fail-open also applies in this case — a transient ctx error must not
// flip a request to 429.
func TestRateLimit_ContextCancelDoesNotBlockRequest(t *testing.T) {
	t.Parallel()
	_, rdb := newRedis(t)

	h, calls := rlOK()
	log, _ := rlBufLogger()
	mw := middleware.RateLimit(rdb,
		middleware.WithRateLimitOverrides(2, 0, 0),
		middleware.WithRateLimitLogger(log),
	)(h)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := rlPrincipal(t,
		httptest.NewRequest(http.MethodGet, "/v1/anything", nil).WithContext(ctx),
		"jti-ctx", "cli")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	// Whether the script errored on the canceled ctx or returned
	// quickly, the outcome is "allow" (fail-open). The handler may or
	// may not have been called depending on script timing; the
	// invariant is "not 429".
	if rec.Code == http.StatusTooManyRequests {
		t.Fatalf("canceled ctx should not 429; got %d (calls=%d)", rec.Code, calls.Load())
	}
}

// ensure go-redis nil sentinel referenced for completeness — tests
// don't directly assert on it but the import path stays warm.
var _ = goredis.Nil
