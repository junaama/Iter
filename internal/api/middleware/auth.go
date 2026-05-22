package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/iter-dev/iter/internal/auth"
	"github.com/iter-dev/iter/pkg/contracts"
)

// Response bodies are intentionally generic. CLAUDE.md security posture
// requires the auth layer to never leak the specific verification failure
// reason to the client; details land in structured logs only.
const (
	// invalidTokenBody is returned for every 401 emitted by the auth
	// middleware regardless of underlying cause (expired, bad signature,
	// missing tenant_id, etc.) so a probing client cannot distinguish
	// "your token is well-formed but expired" from "your token's
	// signature does not match our JWKS."
	invalidTokenBody = `{"error":"invalid_token"}`

	// authUnavailableBody is returned for 503 — distinct from 401 so
	// callers (CLI, dashboard) can backoff-and-retry on infra trouble
	// rather than treating it as a credential problem.
	authUnavailableBody = `{"error":"auth_unavailable"}`

	// bearerScheme is the only Authorization scheme we accept. Case
	// must match per RFC 6750.
	bearerScheme = "Bearer"

	// wwwAuthenticate is the challenge header emitted with every 401
	// so well-behaved clients know what scheme to use.
	wwwAuthenticate = `Bearer realm="iter"`
)

// authOptions captures middleware tuning derived from variadic AuthOption
// values. We use the functional-options pattern (rather than an exported
// config struct) so router.go can express intent inline — "skip these
// paths, log to this logger" — and so the zero value of the middleware
// remains a working default. Adding a knob in a future slice is a
// non-breaking change: new With* helper, new private field.
type authOptions struct {
	// skipPrefixes is the set of URL path prefixes that bypass auth
	// entirely. Whitelist semantics: /health (probe; 030) and
	// /v1/webhooks/* (HMAC-verified separately by 041/042 — JWTs are
	// not in play for third-party callers).
	skipPrefixes []string

	// logger is the slog.Logger used to emit security events on Verify
	// failures. Defaults to slog.Default() so production wiring without
	// an explicit logger still records events; tests inject a buffered
	// logger via WithLogger.
	logger *slog.Logger
}

// AuthOption mutates authOptions. Functional-options pattern (see Dave
// Cheney "Functional options for friendly APIs"). Use WithSkip /
// WithLogger to configure; do not construct authOptions directly.
type AuthOption func(*authOptions)

// WithSkip registers URL path prefixes that bypass auth entirely. Matching
// is exact-prefix (strings.HasPrefix on r.URL.Path). The verifier is NOT
// called for matching requests, so downstream handlers will not see a
// Principal on the context — they must not assume one. Pass an empty list
// (or omit the option) to require auth on every route.
func WithSkip(prefixes ...string) AuthOption {
	return func(o *authOptions) {
		o.skipPrefixes = append(o.skipPrefixes, prefixes...)
	}
}

// WithLogger overrides the slog.Logger used for security events emitted
// by the auth middleware. Production wires deps.Logger; tests inject a
// buffered logger to assert event payloads.
func WithLogger(logger *slog.Logger) AuthOption {
	return func(o *authOptions) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// tokenVerifier is the subset of *auth.Verifier the middleware actually
// uses. Pulling it out as an unexported interface lets the test file
// inject canned Principals / errors without standing up a real JWKS
// server. *auth.Verifier satisfies this interface by virtue of having a
// matching Verify method; no adapter is needed.
type tokenVerifier interface {
	Verify(ctx context.Context, raw string) (contracts.Principal, error)
}

// Auth returns the JWT verification middleware. Per ARCHITECTURE.md §9
// Step 4 the middleware sits between recover and tenant_context in the
// chain (request_id → logger → recover → AUTH → tenant_context →
// rate_limit → idempotency).
//
// Behavior:
//
//  1. Whitelisted paths skip auth entirely — the verifier is not called.
//  2. A nil verifier (env vars unset at boot) short-circuits every
//     non-whitelisted request with 503 auth_unavailable so early-bring-up
//     deploys without WorkOS configured fail loudly but with a clearly
//     distinguishable status from "your token is bad."
//  3. Missing or malformed Authorization header → 401 with the
//     WWW-Authenticate challenge.
//  4. Verify sentinel errors map per the issue 031 contract:
//     - ErrExpired / ErrInvalidClaims / ErrBadSignature / ErrMalformed /
//     ErrMissingTenant / ErrMissingSubject / ErrNotYetValid → 401
//     - ErrAuthUnavailable (JWKS cold cache miss + fetch failure) → 503
//     - anything else (defensive) → 401
//  5. On success the Principal is stashed in the request context via
//     contracts.WithPrincipal so downstream middleware (032 tenant) and
//     handlers can read it via contracts.PrincipalFromContext.
//
// Security event logging: every Verify failure logs at Warn level with
// the sentinel error name, request path, and method. We deliberately do
// NOT log the raw token — even a denied token is bearer credentials.
func Auth(v *auth.Verifier, opts ...AuthOption) Mw {
	// A nil *auth.Verifier must remain nil when handed to the inner
	// implementation: passing it as tokenVerifier would create a typed-
	// nil interface that is != nil at the comparison site. The explicit
	// branch keeps the boot-without-WorkOS path working.
	if v == nil {
		return authMiddleware(nil, opts...)
	}
	return authMiddleware(v, opts...)
}

// authMiddleware is the interface-typed implementation. Exported Auth
// wraps it so callers don't have to depend on tokenVerifier directly,
// and tests can inject a stub by calling authMiddleware from inside the
// middleware package.
func authMiddleware(v tokenVerifier, opts ...AuthOption) Mw {
	cfg := authOptions{logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Whitelist check first so /health and webhook probes never
			// touch the verifier (cheap, allocation-free, and means a
			// nil verifier still passes /health through).
			if isWhitelisted(r.URL.Path, cfg.skipPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			// Nil verifier branch: boot without WORKOS_* env vars yields
			// deps.Auth=nil. Rather than panic or silently allow, we
			// return 503 so the deploy is visible-broken instead of
			// invisible-unsafe.
			if v == nil {
				logSecurityEvent(cfg.logger, r, "auth_verifier_unavailable", nil)
				writeAuthUnavailable(w)
				return
			}

			raw, ok := extractBearer(r.Header.Get("Authorization"))
			if !ok {
				logSecurityEvent(cfg.logger, r, "auth_missing_or_malformed_header", nil)
				writeInvalidToken(w)
				return
			}

			principal, err := v.Verify(r.Context(), raw)
			if err != nil {
				handleVerifyError(w, r, cfg.logger, err)
				return
			}

			ctx := contracts.WithPrincipal(r.Context(), principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isWhitelisted reports whether path begins with any of the configured
// skip prefixes. Linear scan is fine — the whitelist is typically two or
// three entries and runs once per request before any allocation.
func isWhitelisted(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// extractBearer parses an Authorization header value of the form
// "Bearer <token>". Returns (token, true) on success and ("", false) for
// any deviation: empty header, wrong scheme, missing token, or extra
// whitespace.
//
// We split on the first space rather than calling strings.Fields so a
// token containing whitespace (which would itself be malformed but we
// don't want to silently truncate it) reports its actual length to the
// verifier — the verifier returns ErrMalformed and we 401.
func extractBearer(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	// Case-sensitive on scheme per RFC 6750. Lowercased "bearer" is
	// rejected because curl/SDKs reliably emit the canonical form, and
	// permissiveness here invites token-smuggling on non-conforming
	// proxies.
	idx := strings.IndexByte(header, ' ')
	if idx <= 0 {
		return "", false
	}
	if header[:idx] != bearerScheme {
		return "", false
	}
	token := strings.TrimSpace(header[idx+1:])
	if token == "" {
		return "", false
	}
	return token, true
}

// handleVerifyError maps an auth.Verifier sentinel to an HTTP response.
// All 401-mapped errors share the same generic body to avoid leaking the
// failure mode; ErrAuthUnavailable is the only path that returns 503.
func handleVerifyError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, auth.ErrAuthUnavailable):
		logSecurityEvent(logger, r, "auth_jwks_unavailable", err)
		writeAuthUnavailable(w)
	case errors.Is(err, auth.ErrExpired),
		errors.Is(err, auth.ErrNotYetValid),
		errors.Is(err, auth.ErrInvalidClaims),
		errors.Is(err, auth.ErrBadSignature),
		errors.Is(err, auth.ErrMalformed),
		errors.Is(err, auth.ErrMissingTenant),
		errors.Is(err, auth.ErrMissingSubject):
		logSecurityEvent(logger, r, sentinelEventName(err), err)
		writeInvalidToken(w)
	default:
		// Defensive default — an unexpected verifier error is still a
		// failed auth attempt. Map to 401 (not 500) so a probing client
		// can't distinguish unmapped failures from "your token is bad."
		logSecurityEvent(logger, r, "auth_verify_unknown", err)
		writeInvalidToken(w)
	}
}

// sentinelEventName returns a short, stable event name for a verifier
// sentinel error. Used as the slog message so a log query like
// `msg="auth_token_expired"` works without parsing nested error strings.
func sentinelEventName(err error) string {
	switch {
	case errors.Is(err, auth.ErrExpired):
		return "auth_token_expired"
	case errors.Is(err, auth.ErrNotYetValid):
		return "auth_token_not_yet_valid"
	case errors.Is(err, auth.ErrInvalidClaims):
		return "auth_invalid_claims"
	case errors.Is(err, auth.ErrBadSignature):
		return "auth_bad_signature"
	case errors.Is(err, auth.ErrMalformed):
		return "auth_malformed_token"
	case errors.Is(err, auth.ErrMissingTenant):
		return "auth_missing_tenant"
	case errors.Is(err, auth.ErrMissingSubject):
		return "auth_missing_subject"
	}
	// Unreachable by construction: handleVerifyError only calls this
	// for known sentinels (the default arm uses a literal event name).
	// We still return *something* defensively rather than panic so a
	// future refactor that adds a sentinel without updating this
	// function degrades gracefully instead of crashing.
	return "auth_verify_unknown"
}

// logSecurityEvent emits a Warn-level log with a stable event name and a
// minimal payload. We intentionally omit the raw token and any claim
// values — a rejected token is still bearer credentials and PII.
func logSecurityEvent(logger *slog.Logger, r *http.Request, event string, err error) {
	// logger is guaranteed non-nil by authMiddleware's constructor —
	// authOptions.logger defaults to slog.Default() and WithLogger
	// ignores nil values. The defensive nil-check is therefore omitted
	// here; if a future caller bypasses the constructor, the
	// nil-deref will surface as a panic at the call site rather than
	// silently swallowing a security event.
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
	}
	if id, ok := RequestIDFromContext(r.Context()); ok {
		attrs = append(attrs, slog.String("request_id", id))
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	logger.LogAttrs(r.Context(), slog.LevelWarn, event, attrs...)
}

// writeInvalidToken emits the canonical 401 response: WWW-Authenticate
// challenge + generic body.
func writeInvalidToken(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", wwwAuthenticate)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(invalidTokenBody))
}

// writeAuthUnavailable emits the 503 response indicating an auth-backend
// problem (JWKS unreachable, verifier not configured). Distinct status
// from 401 so clients can backoff rather than re-authenticate.
func writeAuthUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(authUnavailableBody))
}
