// Package api hosts the HTTP server's router, handler tree, and middleware
// chain. It is intentionally empty at this slice (issue 048): the skeleton
// only nails down the package address so subsequent slices can fill it.
//
// Planned shape (issue 028 + 029):
//   - NewRouter(deps) returns a chi.Router (router choice locked in
//     DECISIONS.md — github.com/go-chi/chi/v5).
//   - Middleware chain, declared in this order:
//     request_id → logger → auth → tenant_context → rate_limit → idempotency.
//   - HealthHandler returning {"ok": true, "version": <ldflags>} once the
//     binary is wired (issue 028).
//
// Do NOT import chi or any HTTP framework from this slice. The dependency
// lands with issue 028 so go.mod stays clean through issue 048.
package api
