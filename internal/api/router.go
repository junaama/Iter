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
	// (031), tenant context (032), rate limit (033), idempotency
	// (034) — because Railway and BetterStack probe it every 30s
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
		//   request_id → logger → recover → [auth → tenant → rate_limit → idempotency]
		// Auth (031), tenant_context (032), rate_limit (033),
		// idempotency (034) slot in here as later slices land.
		authed.Use(
			middleware.RequestID,
			middleware.Logger(deps.Logger),
			middleware.Recover(deps.Logger),
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
