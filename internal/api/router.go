package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/iter-dev/iter/internal/api/handler"
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

	// GET /health (issue 030) bypasses every middleware below — auth
	// (031), tenant context (034), rate limit (032), idempotency
	// (033) — because Railway and BetterStack probe it every 30s
	// without credentials, per deploy.md §"Healthcheck".
	//
	// chi enforces "all middlewares must be defined before routes on
	// a mux" within a single mux, so we cannot register /health
	// before r.Use(...) on the root router. Instead we install the
	// stack via chi.Group below, which scopes the Use calls to the
	// group's inline subrouter; everything outside the group bypasses
	// the chain. /health is registered on the root router after the
	// group returns so its middleware stack is empty.
	r.Group(func(authed chi.Router) {
		// Middleware stack per ARCHITECTURE.md §9 Step 4:
		//   request_id → logger → recover → auth → tenant → rate_limit → idempotency
		// Auth (031) and idempotency (033) are wired today;
		// tenant_context (034) and rate_limit (032) slot in here
		// as later slices land.
		authed.Use(
			middleware.RequestID,
			middleware.Logger(deps.Logger),
			middleware.Recover(deps.Logger),
			// Auth (031) verifies WorkOS-issued JWTs and stashes a
			// contracts.Principal on the request context for
			// downstream middleware (032/034) and handlers.
			// /v1/webhooks/* (HMAC-verified separately by 041/042)
			// bypasses auth via WithSkip. A nil deps.Auth
			// (WORKOS_* env vars unset at boot) yields 503
			// auth_unavailable on every non-whitelisted request
			// so an under-configured deploy fails visibly rather
			// than silently accepting unauthenticated traffic.
			middleware.Auth(
				deps.Auth,
				middleware.WithSkip("/v1/webhooks"),
				middleware.WithLogger(deps.Logger),
			),
			// Idempotency (033) — caches POST responses by tenant
			// + path + Idempotency-Key for 24h with a 60s
			// in-flight lock. Sits after auth so the Principal it
			// requires is on the context. Webhook routes skip the
			// cache themselves (handler-managed delivery IDs per
			// 041/042); GET/HEAD/OPTIONS/PUT/DELETE/PATCH pass
			// through unchanged. Nil Redis is fail-open per the
			// rate-limit policy in issue 017.
			middleware.Idempotency(deps.Redis, deps.Logger),
		)

		// Placeholder for any unrouted request until handlers land
		// in 029+. 503 (not 404) so misconfigured callers reaching
		// this binary today can distinguish "wrong URL" from
		// "skeleton, no handlers yet".
		authed.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "iter api skeleton — handlers land in issues 029+", http.StatusServiceUnavailable)
		})
	})

	// /health lives on the root router with no middleware applied.
	// Registered AFTER the Group so it's clear visually that it sits
	// outside the chain; chi.Get on the root mux is order-independent
	// with respect to a sibling Group's Use calls.
	r.Get("/health", handler.HealthHandler(deps))

	return r
}
