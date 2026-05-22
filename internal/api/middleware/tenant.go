package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/pkg/contracts"
)

// Tenant middleware (issue 034) opens a tenant-scoped Postgres
// transaction for every authenticated request and stashes the active
// pgx.Tx in the request context so repository functions downstream
// (issue 051+) pick it up via db.FromContext / db.Querier without
// manual plumbing.
//
// Stack position per ARCHITECTURE.md §9 Step 4:
//
//	request_id → logger → recover → auth → TENANT → rate_limit → idempotency
//
// Tenant runs AFTER auth (so contracts.Principal is on the context)
// and BEFORE rate_limit/idempotency. Whitelisted paths — `/health`
// and `/v1/webhooks/*` by default — pass through unchanged because
// the probe (no auth) and webhook handlers (HMAC-verified separately,
// handler-managed tx scope per issues 041/042) must not open a tx on
// their behalf.
//
// Implementation choice — per-request transaction (option A from
// issue 034). The per-statement helper alternative was rejected for v1
// because:
//
//  1. The locked invariant is RLS via SET LOCAL app.current_tenant,
//     and SET LOCAL is scoped to the surrounding tx. Per-statement
//     helpers would need to re-issue SET LOCAL on every call (cheap
//     but easy to forget) and couldn't keep two reads consistent
//     inside one handler (suggest reads embeddings + scores in the
//     same call — ARCHITECTURE.md §5).
//  2. Repository code (issue 051) already accepts a pgx.Tx, so the
//     per-request-tx shape is the path of least surprise.
//  3. Connection-pool pressure at 5K engineers is well under the v1
//     pgxpool default cap; migrating to per-statement is documented
//     in ARCHITECTURE.md §8 as a 25K-scale move.
//
// Commit/rollback policy — the inner handler writes to the
// ResponseWriter BEFORE the middleware learns the final HTTP status
// (net/http has no built-in "intercept then forward" hook). Two
// patterns are viable; this implementation picks (b):
//
//	(a) Buffer the response in httptest.ResponseRecorder, inspect
//	    Code, commit/rollback, then flush. Trade-off: every response
//	    sits in memory until after Commit/Rollback returns. Adds
//	    latency on the p99 suggest path and breaks streaming.
//	(b) Wrap the ResponseWriter with a status-capturing interceptor
//	    that records the first WriteHeader (or 200 if Write was
//	    called without a header). After next.ServeHTTP returns we
//	    have the status; return errTenantRollback from the fn closure
//	    for 4xx/5xx and nil for 2xx/3xx. db.WithTenant commits or
//	    rolls back accordingly.
//
//	    Trade-off accepted: a rolled-back tx + already-flushed
//	    response is OK for v1. The rollback only matters for DB
//	    writes (whose effects are reverted); the response bytes are
//	    already on the wire. A handler returning 500 after a
//	    successful INSERT will have its INSERT reverted, matching
//	    the "transactional handler" intent. The narrow loss is that
//	    a handler returning 4xx after a side-effecting external call
//	    (Stripe charge, send email) still observes those externals
//	    while the DB tx is reverted — consistent with the existing
//	    panic-during-handler behavior under middleware.Recover.
//
// nil pool — pass-through with a warn log per request. Useful for
// boot-without-DB smoke and tests that don't exercise the DB. The
// downstream handler will panic via db.Querier if it actually needs
// the DB; that's a loud, easy-to-diagnose wiring failure rather than a
// silent RLS bypass.

// errTenantRollback is the sentinel returned from the WithTenant
// closure to force a rollback after a 4xx/5xx response. The status
// code has already been written to the wire by the time db.WithTenant
// returns; this sentinel is private to the middleware and never
// reaches the client or downstream handlers.
var errTenantRollback = errors.New("tenant middleware: rollback due to non-2xx response")

// tenantOptions captures middleware tuning derived from variadic
// TenantOption values. Mirrors the authOptions / idempotencyConfig
// shape used elsewhere in this package.
type tenantOptions struct {
	skipPrefixes []string
	logger       *slog.Logger
}

// TenantOption mutates tenantOptions. Functional-options pattern.
type TenantOption func(*tenantOptions)

// WithTenantSkip appends URL path prefixes that bypass the tenant
// middleware. Default skips: /health and /v1/webhooks. Handlers
// downstream of a skipped path MUST NOT call db.Querier (panics
// because no tx is on the ctx). Named WithTenantSkip — not WithSkip —
// because the latter identifier is already taken by Auth in this same
// package.
func WithTenantSkip(prefixes ...string) TenantOption {
	return func(o *tenantOptions) {
		o.skipPrefixes = append(o.skipPrefixes, prefixes...)
	}
}

// WithTenantLogger overrides the slog.Logger used for tenant
// middleware events. Production wires deps.Logger; tests inject a
// buffered logger to assert event payloads. nil is ignored so the
// default slog.Default() remains in force.
func WithTenantLogger(logger *slog.Logger) TenantOption {
	return func(o *tenantOptions) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// statusCaptureWriter is a minimal http.ResponseWriter wrapper that
// remembers the first WriteHeader (or 200 if the handler called Write
// without one). We deliberately do not implement Hijacker / Flusher /
// Pusher: v1 API endpoints are request/response JSON, and a streaming
// response would force us to choose between "buffer until
// commit/rollback" (option (a)) and "let streaming bypass tx
// accounting." If a future slice ships SSE/WebSocket via this stack
// the wrapper grows interface promotion then.
type statusCaptureWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records the status and forwards. Idempotent — the first
// call wins, matching net/http's documented behavior.
func (w *statusCaptureWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

// Write implicitly emits a 200 if the handler called Write without an
// explicit WriteHeader (matching net/http convention).
func (w *statusCaptureWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Tenant returns the per-request RLS-transaction middleware. pool is
// the request-path pgxpool (deps.DB from app.Deps; iter_app role).
// A nil pool yields pass-through with a per-request warn log.
func Tenant(pool *pgxpool.Pool, opts ...TenantOption) Mw {
	cfg := tenantOptions{
		skipPrefixes: []string{
			"/health",
			"/v1/webhooks",
		},
		logger: slog.Default(),
	}
	for _, o := range opts {
		o(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Whitelist check first — cheaper than acquiring a conn.
			if pathHasAnyPrefix(r.URL.Path, cfg.skipPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			// nil pool: pass-through. Warn (not Error) — this is an
			// expected boot mode before DATABASE_URL is provisioned.
			if pool == nil {
				cfg.logger.LogAttrs(r.Context(), slog.LevelWarn,
					"tenant_middleware_nil_pool_passthrough",
					slog.String("path", r.URL.Path))
				next.ServeHTTP(w, r)
				return
			}

			// Principal must be on the ctx by now (Auth — issue 031
			// — wired upstream). Its absence is a router-wiring bug,
			// not a credential problem. 500, not 401, so the failure
			// is visibly internal rather than mistakenly attributed
			// to the caller's token.
			principal, ok := contracts.PrincipalFromContext(r.Context())
			if !ok {
				cfg.logger.LogAttrs(r.Context(), slog.LevelError,
					"tenant_middleware_missing_principal",
					slog.String("path", r.URL.Path),
					slog.String("method", r.Method))
				writeJSON(w, http.StatusInternalServerError, internalErrBody)
				return
			}

			sw := &statusCaptureWriter{ResponseWriter: w}

			// db.WithTenant validates the UUID, opens a tx, runs
			// SET LOCAL app.current_tenant, stashes the tx on the
			// ctx, then commits on nil / rolls back on non-nil from
			// fn or panic. We return errTenantRollback for 4xx/5xx
			// to force the rollback path; the response body is
			// already on the wire (option (b) in the package
			// comment).
			err := db.WithTenant(r.Context(), pool, principal.TenantID.String(),
				func(txCtx context.Context, _ pgx.Tx) error {
					next.ServeHTTP(sw, r.WithContext(txCtx))
					if sw.status >= 400 {
						return errTenantRollback
					}
					return nil
				})

			// errTenantRollback is the expected rollback signal; any
			// OTHER error means WithTenant itself failed (begin, SET
			// LOCAL, commit) or the handler ran but the rollback
			// secondary error wrapped errTenantRollback. Log the
			// real-error case and, if the handler never managed to
			// emit a status (Begin/SET-LOCAL failed before next ran),
			// fall through to a 500.
			if err != nil && !errors.Is(err, errTenantRollback) {
				cfg.logger.LogAttrs(r.Context(), slog.LevelError,
					"tenant_middleware_tx_failed",
					slog.String("path", r.URL.Path),
					slog.String("method", r.Method),
					slog.String("err", err.Error()))
				if !sw.wroteHeader {
					writeJSON(w, http.StatusInternalServerError, internalErrBody)
				}
			}
		})
	}
}
