package middleware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Defaults for the idempotency middleware. Per issue 033 and
// ARCHITECTURE.md §5: cache responses for 24h, hold an in-flight lock
// for 60s while the first request runs the handler, refuse to cache
// payloads larger than 1 MiB.
const (
	DefaultCacheTTL    = 24 * time.Hour
	DefaultLockTTL     = 60 * time.Second
	DefaultWaitTimeout = 30 * time.Second
	DefaultPollEvery   = 50 * time.Millisecond
	DefaultMaxBodyB    = 1 << 20 // 1 MiB
)

// IdempotencyOption configures the middleware. The zero value of
// idempotencyConfig is filled with Default* constants by Idempotency
// before any option runs.
type IdempotencyOption func(*idempotencyConfig)

type idempotencyConfig struct {
	cacheTTL    time.Duration
	lockTTL     time.Duration
	waitTimeout time.Duration
	pollEvery   time.Duration
	maxBodyB    int
	// skipPrefixes are URL path prefixes that bypass the middleware
	// entirely. Webhook endpoints supply their own idempotency keys
	// (X-GitHub-Delivery, Linear-Delivery) — see issues 041/042.
	skipPrefixes []string
}

// WithCacheTTL overrides the 24h response TTL.
func WithCacheTTL(d time.Duration) IdempotencyOption {
	return func(c *idempotencyConfig) { c.cacheTTL = d }
}

// WithLockTTL overrides the 60s in-flight lock TTL.
func WithLockTTL(d time.Duration) IdempotencyOption {
	return func(c *idempotencyConfig) { c.lockTTL = d }
}

// WithWaitTimeout overrides the 30s ceiling on how long a concurrent
// caller blocks waiting for the lock-holder's response.
func WithWaitTimeout(d time.Duration) IdempotencyOption {
	return func(c *idempotencyConfig) { c.waitTimeout = d }
}

// WithPollEvery overrides the cache-poll interval used by concurrent
// callers waiting on the lock holder. Tests use a very small value to
// keep iteration tight.
func WithPollEvery(d time.Duration) IdempotencyOption {
	return func(c *idempotencyConfig) { c.pollEvery = d }
}

// WithMaxBodyBytes overrides the 1 MiB response-body cap. Responses
// larger than the cap fall through uncached.
func WithMaxBodyBytes(n int) IdempotencyOption {
	return func(c *idempotencyConfig) { c.maxBodyB = n }
}

// WithSkipPrefix appends a URL prefix to the skip list. Default skips
// are "/v1/webhooks" (handlers manage their own keys) and "/health"
// (defense in depth — GETs already bypass).
func WithSkipPrefix(prefix string) IdempotencyOption {
	return func(c *idempotencyConfig) { c.skipPrefixes = append(c.skipPrefixes, prefix) }
}

// cachedResponse is the JSON shape persisted in Redis. Body is
// base64-encoded so binary payloads round-trip safely. Headers preserve
// every value emitted by the handler so replays are byte-identical
// (except for the X-Idempotent-Replay marker the middleware adds).
type cachedResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	BodyB64 string              `json:"body_b64"`
}

// missingKeyBody is the canonical 400 payload for POSTs without the
// Idempotency-Key header. The shape mirrors contracts.ErrorResponse
// (`{"error":"..."}`) so CLI/daemon clients can parse it uniformly.
const missingKeyBody = `{"error":"missing_idempotency_key"}`

// internalErrBody is returned when a POST arrives without an
// authenticated Principal on the context. Per issue 033 the
// recommended policy is to refuse: every v1 POST sits behind auth, so
// reaching idempotency without a Principal is a wiring bug.
const internalErrBody = `{"error":"internal"}`

// Idempotency returns a middleware that caches POST responses by
// Idempotency-Key header for 24h. Behavior summary (full spec in issue
// 033):
//
//   - Non-POST methods pass through unchanged.
//   - Skip-list prefixes pass through (defaults: /v1/webhooks, /health).
//   - POST without Idempotency-Key → 400 missing_idempotency_key.
//   - POST without Principal on context → 500 internal. This middleware
//     MUST sit after auth (031) per ARCHITECTURE.md §9 Step 4.
//   - POST with key + cache hit → cached status/headers/body replayed
//     verbatim with X-Idempotent-Replay: true added.
//   - POST with key + no cache → SET NX lock at
//     idempotency:lock:<tenant>:<route>:<key> (60s TTL), run handler,
//     persist response, release lock.
//   - Concurrent POST that loses the SETNX race → polls the cache key
//     every PollEvery (default 50ms) up to WaitTimeout (default 30s).
//     Polling, not pubsub: keeps the dependency surface to the same
//     go-redis primitives the rate-limit + streams paths already use,
//     and miniredis-backed tests don't have to mock subscribe.
//   - Response bodies larger than maxBodyB (default 1 MiB) bypass the
//     cache: handler runs, response is written, no cache entry. A
//     warning is logged so operators notice large endpoints that defeat
//     idempotency.
//
// rdb == nil is INTENTIONAL fail-open (debug-logged once per request).
// This matches the rate-limit fail-open policy (issue 017) — a missing
// Redis must not take the API down in dev where REDIS_URL is unset.
func Idempotency(rdb *goredis.Client, log *slog.Logger, opts ...IdempotencyOption) Mw {
	cfg := idempotencyConfig{
		cacheTTL:    DefaultCacheTTL,
		lockTTL:     DefaultLockTTL,
		waitTimeout: DefaultWaitTimeout,
		pollEvery:   DefaultPollEvery,
		maxBodyB:    DefaultMaxBodyB,
		skipPrefixes: []string{
			"/v1/webhooks",
			"/health",
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	if log == nil {
		log = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Non-POST: bypass entirely. GET/PUT/DELETE/PATCH/HEAD
			// keep their own retry semantics; v1 only mandates
			// idempotency on POST.
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			// Path skip-list: webhook handlers manage their own
			// idempotency (X-GitHub-Delivery, Linear-Delivery).
			if pathHasAnyPrefix(r.URL.Path, cfg.skipPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			// Fail-open if Redis isn't configured. Dev convenience.
			if rdb == nil {
				log.LogAttrs(r.Context(), slog.LevelDebug,
					"idempotency_fail_open_no_redis",
					slog.String("path", r.URL.Path))
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				writeJSON(w, http.StatusBadRequest, missingKeyBody)
				return
			}

			// Principal required. By stack order (auth → tenant →
			// rate_limit → idempotency) it MUST be present; if it
			// isn't, fail closed rather than leak across tenants.
			principal, ok := contracts.PrincipalFromContext(r.Context())
			if !ok {
				log.LogAttrs(r.Context(), slog.LevelError,
					"idempotency_missing_principal",
					slog.String("path", r.URL.Path))
				writeJSON(w, http.StatusInternalServerError, internalErrBody)
				return
			}

			route := routePattern(r)
			cacheKey := buildCacheKey(principal.TenantID.String(), route, key)
			lockKey := buildLockKey(principal.TenantID.String(), route, key)

			ctx := r.Context()

			// Cache hit short-circuit.
			if replayed, err := tryReplay(ctx, rdb, w, cacheKey); err != nil {
				log.LogAttrs(ctx, slog.LevelWarn, "idempotency_cache_read_failed",
					slog.String("err", err.Error()))
				// Fall through — fail-open per the rate-limit policy.
			} else if replayed {
				return
			}

			// Try to take the in-flight lock. Loser polls for the
			// winner's cached response.
			acquired, err := rdb.SetNX(ctx, lockKey, "1", cfg.lockTTL).Result()
			if err != nil {
				log.LogAttrs(ctx, slog.LevelWarn, "idempotency_lock_failed",
					slog.String("err", err.Error()))
				next.ServeHTTP(w, r)
				return
			}

			if !acquired {
				if waitAndReplay(ctx, rdb, w, cacheKey, cfg.waitTimeout, cfg.pollEvery) {
					return
				}
				// Wait timed out — best-effort: run the handler
				// uncached rather than 504. The client retried for
				// safety, not because they're sure nothing happened.
				log.LogAttrs(ctx, slog.LevelWarn,
					"idempotency_wait_timeout_fallthrough",
					slog.String("path", r.URL.Path))
				next.ServeHTTP(w, r)
				return
			}

			// We hold the lock — execute the handler into a
			// recorder so we can persist + replay.
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)

			body := rec.Body.Bytes()
			if len(body) > cfg.maxBodyB {
				log.LogAttrs(ctx, slog.LevelWarn,
					"idempotency_body_too_large",
					slog.String("path", r.URL.Path),
					slog.Int("bytes", len(body)),
					slog.Int("limit", cfg.maxBodyB))
				// Skip cache write but still release the lock so
				// concurrent retries don't sit on a dead lock.
				_ = rdb.Del(ctx, lockKey).Err()
				writeRecorded(w, rec, body, false)
				return
			}

			if err := persist(ctx, rdb, cacheKey, rec, body, cfg.cacheTTL); err != nil {
				log.LogAttrs(ctx, slog.LevelWarn,
					"idempotency_cache_write_failed",
					slog.String("err", err.Error()))
				// Continue — caller still gets the real response.
			}
			// Lock can go now that the entry is durable. Best-effort.
			_ = rdb.Del(ctx, lockKey).Err()

			writeRecorded(w, rec, body, false)
		})
	}
}

// pathHasAnyPrefix returns true if path begins with any of prefixes.
// Empty prefix list yields false (no skips). Pulled out of the request
// hot path so it stays branch-predictable.
func pathHasAnyPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// routePattern returns the route key used in cache + lock keys. We use
// the raw URL path because chi's RoutePattern is populated during
// routing tree traversal and is documented as only safely readable
// AFTER the handler returns — too late for a key that gates lookup.
// v1 POST endpoints (POST /v1/suggest, POST /v1/webhooks/*, etc.) are
// all path-param-free per ARCHITECTURE.md §5, so URL.Path and the
// route pattern coincide in practice. If a future POST grows a path
// param the key will simply be more granular than necessary — never
// less, so the cross-tenant + per-route invariant holds. The chi
// import is kept reserved for the day we move the lookup to the
// post-handler tail.
func routePattern(r *http.Request) string {
	_ = chi.RouteContext // see comment above
	return r.URL.Path
}

// buildCacheKey assembles the canonical Redis key. Format documented in
// issue 033: idempotency:<tenant>:POST:<route>:<key>. The literal
// "POST" is hardwired because only POSTs reach this path; including it
// makes the key self-describing in Redis dumps.
func buildCacheKey(tenant, route, key string) string {
	return "idempotency:" + tenant + ":POST:" + route + ":" + key
}

// buildLockKey returns the SETNX lock key for the same tuple. Separate
// namespace ("idempotency:lock:") so a key collision against the
// response cache is structurally impossible.
func buildLockKey(tenant, route, key string) string {
	return "idempotency:lock:" + tenant + ":POST:" + route + ":" + key
}

// tryReplay returns (true, nil) if a cached response existed and was
// written to w, (false, nil) on cache miss, and (false, err) only when
// the Get itself failed for a non-Nil reason.
func tryReplay(ctx context.Context, rdb *goredis.Client, w http.ResponseWriter, cacheKey string) (bool, error) {
	raw, err := rdb.Get(ctx, cacheKey).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return false, nil
		}
		return false, err
	}
	if err := replayCached(w, raw); err != nil {
		return false, err
	}
	return true, nil
}

// waitAndReplay polls the cache key every pollEvery until either a
// cached response appears (returns true) or wait expires (returns
// false). Polling — rather than pubsub — keeps the implementation
// surface to the same go-redis primitives used elsewhere; with
// pollEvery=50ms a 1s suggest budget eats at most 20 polls, well within
// the per-call timeout.
func waitAndReplay(ctx context.Context, rdb *goredis.Client, w http.ResponseWriter, cacheKey string, wait, every time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		raw, err := rdb.Get(ctx, cacheKey).Bytes()
		if err == nil {
			if replayErr := replayCached(w, raw); replayErr == nil {
				return true
			}
			return false
		}
		if !errors.Is(err, goredis.Nil) {
			// Transient — let the outer caller fall through.
			return false
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(every):
		}
	}
}

// replayCached unmarshals raw and writes it to w, adding the
// X-Idempotent-Replay marker. Headers are restored verbatim so clients
// observing e.g. Cache-Control or Content-Type see exactly what the
// original handler emitted.
func replayCached(w http.ResponseWriter, raw []byte) error {
	var cr cachedResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return err
	}
	body, err := base64.StdEncoding.DecodeString(cr.BodyB64)
	if err != nil {
		return err
	}
	for k, vs := range cr.Headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Idempotent-Replay", "true")
	status := cr.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}

// persist serializes the recorded response and SETs it with TTL. The
// header map is copied so subsequent header writes (e.g. by an outer
// middleware) don't mutate the cached value.
func persist(ctx context.Context, rdb *goredis.Client, cacheKey string, rec *httptest.ResponseRecorder, body []byte, ttl time.Duration) error {
	headers := make(map[string][]string, len(rec.Header()))
	for k, vs := range rec.Header() {
		copied := make([]string, len(vs))
		copy(copied, vs)
		headers[k] = copied
	}
	cr := cachedResponse{
		Status:  rec.Code,
		Headers: headers,
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
	payload, err := json.Marshal(cr)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, cacheKey, payload, ttl).Err()
}

// writeRecorded copies the recorded response onto the real
// ResponseWriter. replay=true adds the X-Idempotent-Replay marker; the
// lock-holder path uses replay=false (its caller is the original, not
// a replay).
func writeRecorded(w http.ResponseWriter, rec *httptest.ResponseRecorder, body []byte, replay bool) {
	for k, vs := range rec.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if replay {
		w.Header().Set("X-Idempotent-Replay", "true")
	}
	status := rec.Code
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeJSON emits a small literal JSON payload. Used for the canned
// 400 / 500 short-circuits where buildJSON-from-struct would be
// overkill.
func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
