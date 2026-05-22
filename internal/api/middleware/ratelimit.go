package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Per-token rate-limit defaults. Per ARCHITECTURE.md §5 / DECISIONS.md
// Phase 5: 100/min CLI, 600/min daemon, per token. Window is fixed at
// 60s — the sliding-log algorithm uses unix-ms scores so finer windows
// would just need a different ZREMRANGEBYSCORE bound, but v1 only
// commits to "per minute" semantics.
const (
	// RateLimitWindow is the sliding window size. Constant rather than
	// an option because the spec ties the per-minute numbers to a 60s
	// window — making this configurable invites accidental divergence
	// between the limit and what "/min" means to a client.
	RateLimitWindow = 60 * time.Second

	// DefaultCLILimit applies to JWTs with token_type=cli (interactive
	// `iter suggest` calls — bursty but bounded).
	DefaultCLILimit = 100

	// DefaultDaemonLimit applies to token_type=daemon (background WS
	// gateway, webhooks proxied through the daemon — higher tolerance
	// for the continuous ingestion path).
	DefaultDaemonLimit = 600

	// DefaultFallbackLimit is used when token_type is empty or
	// unrecognized. The conservative default is the CLI ceiling so a
	// misconfigured token can't burst at the daemon rate.
	DefaultFallbackLimit = DefaultCLILimit

	// tokenTypeCLI / tokenTypeDaemon match the WorkOS claim values.
	tokenTypeCLI    = "cli"
	tokenTypeDaemon = "daemon"

	// keyPrefix scopes ZSET keys so rate-limit state never collides
	// with idempotency or stream-DLQ keys.
	keyPrefix = "ratelimit:"

	// rateLimitedBody is the canonical 429 response. Per the issue 032
	// spec the configured limit (N) is allowed in the payload — it IS
	// the limit the caller tripped, so leaking it adds no signal an
	// attacker couldn't already derive from bisecting their own rate.
	rateLimitedBodyFmt = `{"error":"rate_limited","limit":%d,"window_seconds":%d}`
)

// rateLimitScript is the atomic check-and-record Lua script. Single RTT,
// no MULTI/WATCH dance — Lua scripts on Redis run with EVAL exclusivity,
// so two parallel callers on the same key serialize through the script
// boundary. Returns a {allowed, oldest_score_ms} pair where allowed is
// 1 when the request was admitted to the bucket and 0 when it was
// rejected as over-limit. The oldest score is used by the middleware to
// compute Retry-After on the 429 path.
//
// KEYS[1] = ratelimit:<token_id>
// ARGV[1] = now_ms
// ARGV[2] = window_ms
// ARGV[3] = limit (integer; capacity of the bucket)
// ARGV[4] = entry value (e.g. request id — must be unique per request,
//
//	otherwise ZADD treats it as an update and ZCARD undercounts)
//
// Steps:
//  1. Evict entries older than now_ms - window_ms.
//  2. ZCARD to inspect current count.
//  3. If count >= limit → return {0, oldest_score} WITHOUT adding
//     the new entry. The caller should not be allowed to extend the
//     window with another late entry — that would deny-of-service a
//     well-behaved client whose ZSET was already full of legitimate
//     entries.
//  4. Else ZADD the new entry, PEXPIRE the key (window + 1s safety
//     margin), return {1, oldest_score (post-insert)}.
//
// Returning a binary allowed/rejected flag (rather than a count) keeps
// the middleware comparison trivially correct — there's no off-by-one
// between "count after add" vs "count before add" semantics to chase
// across two languages.
const rateLimitScript = `
local key       = KEYS[1]
local now_ms    = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local limit     = tonumber(ARGV[3])
local member    = ARGV[4]

local cutoff = now_ms - window_ms
redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
local count = redis.call('ZCARD', key)

-- Format the oldest score as a fixed-point string. ZRANGE WITHSCORES
-- returns scores via Lua's default tostring, which collapses large
-- integers (e.g. unix-ms timestamps) to scientific notation under some
-- Redis-compatible runtimes (notably miniredis used in tests). Going
-- through tonumber+string.format keeps the on-wire value precise and
-- byte-identical across runtimes.
local oldest = '0'
if count > 0 then
  local oldest_pair = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  if oldest_pair and oldest_pair[2] then
    local n = tonumber(oldest_pair[2])
    if n then
      oldest = string.format('%.0f', n)
    end
  end
end

if count >= limit then
  return {0, oldest}
end

redis.call('ZADD', key, now_ms, member)
if oldest == '0' then
  oldest = string.format('%.0f', now_ms)
end
redis.call('PEXPIRE', key, window_ms + 1000)
return {1, oldest}
`

// rateLimitOptions captures middleware tuning. Functional-options
// pattern mirrors auth.go / idempotency.go so the router wiring reads
// uniformly.
type rateLimitOptions struct {
	// skipPrefixes are URL path prefixes that bypass the limiter. Per
	// the issue 032 spec /health and /v1/webhooks bypass — health is a
	// credential-less probe and webhook auth is HMAC, not JWT, so the
	// per-token bucket doesn't apply.
	skipPrefixes []string

	// logger is the slog.Logger used for fail-open + 429 events.
	// Defaults to slog.Default() so production without an explicit
	// logger still records events; tests inject a buffered logger.
	logger *slog.Logger

	// cliLimit / daemonLimit / fallbackLimit override the defaults.
	// Tests use small values (e.g. 3) to keep iteration tight; v1
	// production keeps the doc'd defaults.
	cliLimit      int
	daemonLimit   int
	fallbackLimit int

	// now is an injectable clock for tests. Defaults to time.Now.
	// Allows the window-slide test to drive miniredis's clock and our
	// clock together without sleeping.
	now func() time.Time
}

// RateLimitOption mutates rateLimitOptions.
type RateLimitOption func(*rateLimitOptions)

// WithRateLimitSkip registers URL path prefixes that bypass the
// limiter. Matching is exact-prefix on r.URL.Path (HasPrefix).
func WithRateLimitSkip(prefixes ...string) RateLimitOption {
	return func(o *rateLimitOptions) {
		o.skipPrefixes = append(o.skipPrefixes, prefixes...)
	}
}

// WithRateLimitLogger overrides the slog.Logger used for fail-open and
// 429 events.
func WithRateLimitLogger(logger *slog.Logger) RateLimitOption {
	return func(o *rateLimitOptions) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// WithRateLimitOverrides replaces the per-token-type ceilings. Pass
// zero for any field to leave the default in place. Tests use this to
// drop the limit to a handful so the over-limit path is reachable
// without flooding miniredis with a thousand entries.
func WithRateLimitOverrides(cli, daemon, fallback int) RateLimitOption {
	return func(o *rateLimitOptions) {
		if cli > 0 {
			o.cliLimit = cli
		}
		if daemon > 0 {
			o.daemonLimit = daemon
		}
		if fallback > 0 {
			o.fallbackLimit = fallback
		}
	}
}

// WithRateLimitClock overrides the time source. Tests inject a
// deterministic clock so the window-slide assertions don't depend on
// wall-clock time.
func WithRateLimitClock(now func() time.Time) RateLimitOption {
	return func(o *rateLimitOptions) {
		if now != nil {
			o.now = now
		}
	}
}

// RateLimit returns the per-token sliding-window middleware. Sits
// AFTER auth (031) and tenant_context (034), BEFORE idempotency (033)
// in the documented stack (ARCHITECTURE.md §9 Step 4):
//
//	request_id → logger → recover → auth → tenant_context → rate_limit → idempotency
//
// Behavior:
//
//  1. Whitelisted paths skip entirely. The default skip list is empty
//     so router.go MUST pass WithRateLimitSkip("/health",
//     "/v1/webhooks") at wire time.
//  2. Missing Principal → bypass. The auth middleware (031) is
//     responsible for rejecting unauthenticated requests; reaching the
//     limiter without one means we're on a whitelisted-by-auth path
//     (e.g. the auth layer's own skip list) and per-token enforcement
//     has nothing to key on.
//  3. nil rdb OR Redis errors → fail OPEN (allow the request) and log
//     ratelimit_redis_unavailable. Per DECISIONS.md "Rate-limit
//     middleware" — deliberate so a Redis outage doesn't take the
//     suggest path down.
//  4. ZSET-backed sliding log per token. Atomic via Lua script so a
//     race between two concurrent requests on the same token cannot
//     both pass at the boundary.
//  5. Over-limit → 429 with Retry-After (seconds until the oldest
//     entry falls off the window) and the documented JSON body.
//
// The limit derivation prefers Principal.TokenType (set by the
// verifier from the WorkOS `token_type` claim). Empty or unrecognized
// values fall back to the configured fallbackLimit (100/min by default
// — never the daemon ceiling, so a misconfigured token can't burst).
func RateLimit(rdb *goredis.Client, opts ...RateLimitOption) Mw {
	cfg := rateLimitOptions{
		logger:        slog.Default(),
		cliLimit:      DefaultCLILimit,
		daemonLimit:   DefaultDaemonLimit,
		fallbackLimit: DefaultFallbackLimit,
		now:           time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Pre-compile the script handle so EVALSHA is preferred and the
	// raw script body only crosses the wire on the first invocation
	// per Redis instance. go-redis falls back to EVAL automatically on
	// NOSCRIPT, so cold replicas (e.g. after a restart) self-heal.
	script := goredis.NewScript(rateLimitScript)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip-list check first so the limiter does no work for
			// /health probes or webhook deliveries.
			if pathHasAnyPrefix(r.URL.Path, cfg.skipPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			// Bypass when no Principal is on the context. The auth
			// middleware fronts every non-whitelisted route in
			// production wiring; reaching here without one means
			// either (a) we're on an auth-whitelisted path that
			// didn't end up in the rate-limit skip list (defensive),
			// or (b) test wiring is exercising the limiter standalone.
			// Either way, per-token enforcement has nothing to key on.
			principal, ok := contracts.PrincipalFromContext(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			limit := limitForTokenType(principal.TokenType, cfg)
			bucketKey := buildRateLimitKey(principal.TokenID, principal.UserID.String())

			// Fail-open guard: a nil client (REDIS_URL unset at boot)
			// must not break the request path. Log once per request
			// so operators see the degraded state without spamming a
			// hot loop.
			if rdb == nil {
				logRateLimitUnavailable(r, cfg.logger, "no_client", nil)
				next.ServeHTTP(w, r)
				return
			}

			now := cfg.now()
			nowMs := now.UnixMilli()
			windowMs := RateLimitWindow.Milliseconds()
			member := rateLimitMember(r, nowMs)

			res, err := script.Run(
				r.Context(),
				rdb,
				[]string{bucketKey},
				nowMs,
				windowMs,
				limit,
				member,
			).Result()
			if err != nil {
				// Redis-side failure: fail open per DECISIONS.md.
				// Mapping a transient infra error to 429 would cause
				// a brown-out far worse than a brief over-burst.
				logRateLimitUnavailable(r, cfg.logger, "script_error", err)
				next.ServeHTTP(w, r)
				return
			}

			allowed, oldestMs, parseErr := parseScriptResult(res)
			if parseErr != nil {
				logRateLimitUnavailable(r, cfg.logger, "script_result", parseErr)
				next.ServeHTTP(w, r)
				return
			}

			if allowed == 0 {
				retryAfter := computeRetryAfter(nowMs, oldestMs, windowMs)
				logRateLimitExceeded(r, cfg.logger, principal, limit)
				writeRateLimited(w, limit, retryAfter)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// limitForTokenType picks the bucket size for this principal. Returns
// the configured ceiling for "cli" / "daemon"; falls back for empty or
// unrecognized values. Conservative: never returns the daemon ceiling
// for an unknown claim — a misconfigured WorkOS connection would
// otherwise hand out the higher limit to every token.
func limitForTokenType(tokenType string, cfg rateLimitOptions) int {
	switch tokenType {
	case tokenTypeCLI:
		return cfg.cliLimit
	case tokenTypeDaemon:
		return cfg.daemonLimit
	default:
		return cfg.fallbackLimit
	}
}

// buildRateLimitKey assembles the Redis key. Prefers TokenID (jti);
// falls back to a sha256 of the user id for tokens that lack jti — the
// fallback is keyed by user so the limit still applies per-identity
// rather than degrading to a single global bucket.
func buildRateLimitKey(tokenID, userID string) string {
	if tokenID != "" {
		return keyPrefix + tokenID
	}
	// Hash the user id so the resulting key shape (hex string) is
	// indistinguishable from a JWT jti at the Redis layer — operators
	// monitoring key patterns don't have to special-case the fallback.
	sum := sha256.Sum256([]byte(userID))
	return keyPrefix + "u-" + hex.EncodeToString(sum[:8])
}

// rateLimitMember returns the ZSET member value for this request.
// Uniqueness is required — ZADD on a duplicate member updates its
// score rather than inserting a new entry, which would silently
// undercount. We prefer the X-Request-ID minted by the RequestID
// middleware (always unique per request) and fall back to
// timestamp+random suffix for the rare case the middleware was not
// installed in this chain (tests that exercise rate_limit standalone).
func rateLimitMember(r *http.Request, nowMs int64) string {
	if id, ok := RequestIDFromContext(r.Context()); ok && id != "" {
		return id
	}
	return strconv.FormatInt(nowMs, 10) + ":" + newULID()
}

// parseScriptResult decodes the {allowed, oldest_score_ms} return
// shape from rateLimitScript. go-redis returns Lua tables as
// []interface{}; each element is either int64 or string depending on
// Lua return type.
func parseScriptResult(res any) (allowed int64, oldestMs int64, err error) {
	arr, ok := res.([]any)
	if !ok || len(arr) < 2 {
		return 0, 0, fmt.Errorf("ratelimit: unexpected script result shape: %T", res)
	}

	switch v := arr[0].(type) {
	case int64:
		allowed = v
	case string:
		c, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return 0, 0, fmt.Errorf("ratelimit: bad allowed: %w", perr)
		}
		allowed = c
	default:
		return 0, 0, fmt.Errorf("ratelimit: unexpected allowed type %T", arr[0])
	}

	switch v := arr[1].(type) {
	case int64:
		oldestMs = v
	case string:
		o, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			// Treat unparseable oldest as "no data" — Retry-After
			// falls back to the full window in that case.
			return allowed, 0, nil
		}
		oldestMs = o
	default:
		return allowed, 0, nil
	}
	return allowed, oldestMs, nil
}

// computeRetryAfter returns the number of seconds until the oldest
// in-window entry rolls off. Rounded up so a sub-second remainder
// still asks the client to wait at least one second — RFC 7231 says
// Retry-After is a positive integer seconds.
func computeRetryAfter(nowMs, oldestMs, windowMs int64) int {
	if oldestMs <= 0 {
		return int((windowMs + 999) / 1000)
	}
	remainingMs := (oldestMs + windowMs) - nowMs
	if remainingMs <= 0 {
		return 1
	}
	secs := (remainingMs + 999) / 1000
	if secs <= 0 {
		return 1
	}
	return int(secs)
}

// writeRateLimited emits the 429 response with the canonical body and
// Retry-After header.
func writeRateLimited(w http.ResponseWriter, limit, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)
	body := fmt.Sprintf(rateLimitedBodyFmt, limit, int(RateLimitWindow.Seconds()))
	_, _ = w.Write([]byte(body))
}

// logRateLimitUnavailable records the fail-open path so operators can
// alert on sustained Redis outages without parsing every miss. Stable
// event name "ratelimit_redis_unavailable" per the issue 032 contract.
func logRateLimitUnavailable(r *http.Request, logger *slog.Logger, reason string, err error) {
	if logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("reason", reason),
	}
	if id, ok := RequestIDFromContext(r.Context()); ok {
		attrs = append(attrs, slog.String("request_id", id))
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	logger.LogAttrs(r.Context(), slog.LevelWarn, "ratelimit_redis_unavailable", attrs...)
}

// logRateLimitExceeded records the 429. Info level (not Warn) because
// a single tenant hitting their ceiling is expected behavior; an alert
// would be tuned on the rate of this event, not its presence.
func logRateLimitExceeded(r *http.Request, logger *slog.Logger, p contracts.Principal, limit int) {
	if logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("token_id", p.TokenID),
		slog.String("token_type", p.TokenType),
		slog.Int("limit", limit),
	}
	if id, ok := RequestIDFromContext(r.Context()); ok {
		attrs = append(attrs, slog.String("request_id", id))
	}
	logger.LogAttrs(r.Context(), slog.LevelInfo, "ratelimit_exceeded", attrs...)
}

// pathHasAnyPrefix is shared with idempotency.go (same package) — see
// the definition there.
