package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/iter-dev/iter/internal/api/authz"
	"github.com/iter-dev/iter/internal/api/handler"
	"github.com/iter-dev/iter/internal/api/middleware"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/ws"
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
		// All five concrete middlewares are wired below — 031 (Auth),
		// 034 (Tenant), 032 (RateLimit), 033 (Idempotency).
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
			// Tenant (034) — opens a per-request RLS-scoped Postgres
			// tx, runs SET LOCAL app.current_tenant from the
			// Principal, and stashes the pgx.Tx on the ctx via
			// internal/db so repository functions (issue 051+) read
			// it implicitly. Commits on 2xx/3xx, rolls back on
			// 4xx/5xx — status captured via a tiny ResponseWriter
			// wrapper because net/http has no native intercept hook.
			// /health and /v1/webhooks/* bypass: the probe has no
			// Principal, and webhook handlers (issues 041/042)
			// manage their own tx scope after HMAC verification.
			// A nil deps.DB passes through with a warn log so
			// early bring-up before DATABASE_URL is provisioned
			// still boots; handlers that require the DB panic via
			// db.Querier with a clear message rather than silently
			// bypass RLS.
			middleware.Tenant(
				deps.DB,
				middleware.WithTenantSkip("/v1/webhooks"),
				middleware.WithTenantLogger(deps.Logger),
			),
			// Authz (admin cache) — installs a per-request cache for
			// tenant_users role checks. Route gates and handlers that
			// branch on admin status call the same DB-backed helper.
			authz.AdminCache,
			// RateLimit (032) — per-token sliding-window limiter
			// keyed by JWT jti. Sits after auth so the Principal
			// (TokenID + TokenType) is on the context, and before
			// idempotency so a 429 short-circuits without touching
			// the idempotency cache. /health and /v1/webhooks/*
			// skip per the issue 032 contract (probe path is
			// credential-less; webhook auth is HMAC, not JWT, so
			// per-token enforcement doesn't apply). Nil Redis
			// fails open per DECISIONS.md "Rate-limit middleware".
			middleware.RateLimit(
				deps.Redis,
				middleware.WithRateLimitSkip("/health", "/v1/webhooks"),
				middleware.WithRateLimitLogger(deps.Logger),
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

		authed.Get("/v1/stack/me", handler.StackMeHandler(deps))
		authed.Get("/v1/stack/{user_id}", handler.StackUserHandler(deps))
		authed.Post("/v1/stack", handler.StackCreateHandler(deps))
		authed.Post("/v1/stack/{id}/share", handler.StackShareHandler(deps))
		authed.Delete("/v1/stack/{id}/share/{user_id}", handler.StackUnshareHandler(deps))

		authed.Get("/v1/dashboard/me", handler.DashboardMeHandler(deps))
		authed.Get("/v1/sessions/{id}", handler.SessionDetailHandler(deps))
		authed.Get("/v1/scores/{session_id}", handler.SessionScoresHandler(deps))
		authed.Get("/v1/sessions", handler.ListSessionsHandler(deps))

		// GET /v1/dashboard/team (issue 039) — team-wide aggregates.
		// The route-level admin gate runs after auth + tenant context
		// so it can verify the caller's tenant_users role inside the
		// same RLS-scoped request transaction.
		authed.With(requireAdmin(deps.Logger)).Get("/v1/dashboard/team", handler.DashboardTeamHandler(deps))

		// POST /v1/suggest (issue 035) is the flagship latency-critical
		// path and intentionally sits inside the full authenticated stack:
		// auth -> tenant tx -> rate limit -> idempotency -> handler.
		authed.Post("/v1/suggest", handler.SuggestHandler(deps))

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

	// POST /v1/webhooks/github (issue 041) sits OUTSIDE the authed
	// Group. The handler authenticates each delivery via per-sender
	// shared-secret HMAC (X-Hub-Signature-256) — not a JWT — and
	// manages its own idempotency keyed by X-GitHub-Delivery. Stacking
	// the chi auth/tenant/idempotency chain on top would either reject
	// every delivery (no JWT, no tenant context) or double-cache with
	// conflicting keys. The middleware chain on the authed Group still
	// declares /v1/webhooks in its skip-list as defense-in-depth, but
	// registering here means the request never enters that Group's
	// subrouter in the first place.
	r.Post("/v1/webhooks/github", handler.GitHubWebhookHandler(deps))

	// POST /v1/webhooks/linear (issue 042) shares the same public
	// receiver posture as GitHub: no JWT middleware, per-source HMAC
	// (Linear-Signature), and delivery-id idempotency
	// (Linear-Delivery) inside the handler.
	r.Post("/v1/webhooks/linear", handler.LinearWebhookHandler(deps))

	// /v1/ws (issue 043) — WebSocket upgrade for the daemon ↔ cloud
	// transport. Lives on the root router (outside the authed Group)
	// because the gateway authenticates BEFORE the upgrade by
	// reading the JWT from either the Authorization header (daemon)
	// or the Sec-WebSocket-Protocol header (browser); the HTTP auth
	// middleware would require Authorization, which browsers cannot
	// set from JS. The gateway also owns its own connection
	// lifecycle (heartbeat, ack protocol) that the request-scoped
	// chi middleware chain isn't shaped for.
	if deps.WS != nil {
		r.Get("/v1/ws", deps.WS.ServeHTTP)
	}

	// AuthKit login flow routes (GET /auth/login, /auth/callback,
	// /auth/logout). Lives on the root router outside the authed
	// Group because these are the endpoints that OBTAIN credentials
	// in the first place — requiring auth here would be circular.
	// The routes redirect through WorkOS-hosted pages and set
	// session cookies on successful authentication. Nil when the
	// WORKOS_* env vars are incomplete (early bring-up / non-prod
	// without WorkOS).
	if deps.AuthKit != nil {
		deps.AuthKit.RegisterRoutes(r)
	}

	return r
}

// Ensure the ws package import is exercised even when deps.WS is nil
// at compile time (early bring-up). Keeps `goimports -w` from
// removing the import after a stale go.mod update.
var _ = ws.NewGateway
