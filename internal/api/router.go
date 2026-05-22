package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/iter-dev/iter/internal/api/middleware"
	"github.com/iter-dev/iter/internal/app"
)

// NewRouter constructs the chi-backed HTTP handler tree.
//
// Subsequent slices (029–047) register routes by calling Route / Mount /
// Method on the returned chi.Router. The returned value also satisfies
// http.Handler so cmd/server can pass it straight to *http.Server. The
// concrete return type is intentionally chi.Router (not http.Handler) so
// per-route registration in later issues stays type-safe without an
// upcast at every call site.
//
// Keep this signature stable: 029–047 issue bodies promise route
// registration will not churn `NewRouter` itself.
func NewRouter(deps app.Deps) chi.Router {
	r := chi.NewRouter()

	// Middleware stack per ARCHITECTURE.md §9 Step 4:
	//   request_id → logger → recover → [auth → tenant → rate_limit → idempotency]
	// Auth (031), tenant_context (032), rate_limit (033), idempotency (034)
	// slot in here as later slices land. We register via chi's Use rather
	// than wrapping the returned handler so subsequent per-route Use calls
	// nest under this base chain.
	r.Use(
		middleware.RequestID,
		middleware.Logger(deps.Logger),
		middleware.Recover(deps.Logger),
		// Auth (031) verifies WorkOS-issued JWTs and stashes a
		// contracts.Principal on the request context for downstream
		// middleware (032 tenant_context, 033 idempotency) and
		// handlers. /health (030) and /v1/webhooks/* (HMAC-verified
		// separately by 041/042) bypass auth via WithSkip. A nil
		// deps.Auth (WORKOS_* env vars unset at boot) yields 503
		// auth_unavailable on every non-whitelisted request so an
		// under-configured deploy fails visibly rather than silently
		// accepting unauthenticated traffic.
		middleware.Auth(
			deps.Auth,
			middleware.WithSkip("/health", "/v1/webhooks"),
			middleware.WithLogger(deps.Logger),
		),
		// Idempotency (033) — caches POST responses by tenant + route
		// + Idempotency-Key for 24h with a 60s in-flight lock. Sits
		// after auth so the Principal it requires is on the context.
		// Webhook routes skip the cache themselves (handler-managed
		// delivery IDs per 041/042) and /health is a GET. Nil Redis
		// is fail-open per the rate-limit policy.
		middleware.Idempotency(deps.Redis, deps.Logger),
	)

	// Placeholder for any unrouted request until handlers land in 029+.
	// 503 (not 404) so misconfigured callers reaching this binary today
	// can distinguish "wrong URL" from "skeleton, no handlers yet".
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "iter api skeleton — handlers land in issues 029+", http.StatusServiceUnavailable)
	})

	return r
}
