// Package api hosts the HTTP server's router, handler tree, and middleware
// chain.
//
// At issue 028 it ships the chi router skeleton:
//   - NewRouter(deps) returns a chi.Router as http.Handler. No routes
//     registered yet beyond a placeholder so we can probe with curl.
//   - Server wraps the http.Handler in a *http.Server with read / write /
//     idle timeouts and exposes Run / Shutdown for cmd/server.
//
// Middleware chain (request_id → logger → recover → auth → tenant_context →
// rate_limit → idempotency) lands in subsequent slices starting at issue
// 029. Handlers (/health, /v1/suggest, dashboard, webhooks, WS) follow in
// 030–043 per ARCHITECTURE.md §9 Step 4.
//
// Do NOT import github.com/go-chi/chi/v5/middleware — middleware concerns
// live under internal/api/middleware/ so the router lock-in stays low and
// we can swap chi for another mux later without rewriting handlers.
package api
