// Package app holds process-level wiring shared by cmd/server and the
// internal HTTP/WS handlers — primarily the Deps struct that the boot
// entry point constructs and threads through NewRouter (issue 028).
//
// Deps is intentionally tiny at issue 048: only what cmd/server itself
// needs to boot (logger, build version). Later slices grow it:
//   - issue 049 adds *pgxpool.Pool (DB) and BatchDB *pgxpool.Pool.
//   - issue 050 added a *redis.Client (Redis Streams + cache).
//   - issue 055 added an *llm.Router (multi-provider LLM).
//   - issue 054 added an *embed.Router (multi-provider embeddings).
//   - issue 056 adds an *auth.Verifier (WorkOS JWT verifier).
//   - issue 057 adds a *modal.Client (scoring stub).
//
// Keeping wiring in one struct (rather than passing each dep individually)
// makes it cheap to grow without touching every handler signature.
package app

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/auth"
	"github.com/iter-dev/iter/internal/embed"
	"github.com/iter-dev/iter/internal/llm"
	"github.com/iter-dev/iter/internal/ws"

	goredis "github.com/redis/go-redis/v9"
)

// Deps is the process-level dependency bag wired by cmd/server at boot and
// passed to api.NewRouter (issue 028) and any other top-level constructors.
//
// All fields must be safe for concurrent use; handlers retain shared refs
// for the lifetime of the process.
type Deps struct {
	// Logger is the structured logger used by every handler. Required.
	Logger *slog.Logger

	// BuildVersion is the value injected at link time via
	//   -ldflags "-X main.version=$(git describe --tags --dirty)"
	// and surfaced by the /health endpoint. Empty string is allowed in
	// local `go run` builds and renders as "dev" in the health payload.
	BuildVersion string

	// DB is the request-path Postgres pool, built from $DATABASE_URL
	// (iter_app role, NOBYPASSRLS). All tenant-scoped queries flow
	// through db.WithTenant(ctx, DB, tenantID, fn) so that the
	// `SET LOCAL app.current_tenant` GUC is set inside the same tx as
	// the query. Required for any handler that touches Postgres;
	// optional at boot only for cmd/server smoke tests.
	DB *pgxpool.Pool

	// BatchDB is the BYPASSRLS Postgres pool, built from
	// $DATABASE_URL_BATCH (iter_batch role). ONLY for cross-tenant
	// jobs: nightly scoring (issue 046, owned by the Modal worker which
	// holds its own Python-side iter_batch connection) and the archive
	// cron (issue 047, which consumes THIS pool). Never reachable from
	// the request path. cmd/server populates BatchDB when
	// DATABASE_URL_BATCH is set; when unset, the archive cron is
	// skipped (warned, not fatal) so dev boots without R2 configuration
	// still come up.
	BatchDB *pgxpool.Pool

	// LLM is the multi-provider router (issue 055). May be nil in tests
	// that don't exercise the suggest path; handlers that require it
	// must nil-check and return 503 (mapped to `no_suggestion_reason:
	// llm_unavailable` by the suggest handler, ARCHITECTURE.md §7).
	LLM *llm.Router

	// Embed is the multi-provider embedding router (issue 054). May be
	// nil in tests; the embedding worker and suggest-path cache miss
	// nil-check at use site. When unavailable the embedding worker
	// requeues with backoff and the session is viewable but not
	// searchable until embedding lands (ARCHITECTURE.md §7 "Embedding
	// provider unavailable").
	Embed *embed.Router

	// Redis is the cache + Redis Streams client (issue 050). Optional:
	// some workloads (e.g. a pure migration runner sub-command or a
	// local-only smoke build before REDIS_URL is provisioned) can run
	// without Redis. Components that require it MUST nil-check at use
	// site and return a clear error rather than panicking; cmd/server
	// logs a warning at boot when REDIS_URL is unset.
	Redis *goredis.Client

	// Auth is the WorkOS JWT verifier (issue 056) consumed by the auth
	// middleware (issue 031). May be nil in non-prod boots when the
	// WORKOS_* env vars are unset; the middleware nil-checks and
	// returns 503 auth_unavailable on every non-whitelisted request so
	// early-bring-up is loud rather than silently-unauthenticated.
	Auth *auth.Verifier

	// WS is the WebSocket gateway (issue 043) registered on GET /v1/ws.
	// The gateway authenticates inside ServeHTTP (NOT via the HTTP
	// auth middleware) because the JWT may arrive via either
	// Authorization or Sec-WebSocket-Protocol depending on whether
	// the client is the daemon or a browser. Nil in tests / early
	// boots that don't require WS — the router skips the route
	// registration when WS is nil rather than serve a half-wired
	// handler.
	WS *ws.Gateway

	// AuthKit provides the redirect-based login flow (GET /auth/login,
	// /auth/callback, /auth/logout) that issues the JWTs validated by
	// deps.Auth. Nil when WORKOS_API_KEY / WORKOS_CLIENT_ID /
	// WORKOS_REDIRECT_URI are unset — the routes are not registered and
	// users cannot authenticate via the web UI, only via device-code
	// flow (daemon/CLI, issue 058+).
	AuthKit *auth.AuthKit

	// WebhookSecrets bundles the per-source shared secrets used for
	// HMAC verification by the webhook handlers (issues 041/042).
	// Unset secrets cause the handler to reject every delivery with
	// 401 — a webhook handler with no secret is a wide-open ingress
	// and must fail closed.
	WebhookSecrets WebhookSecrets
	//   Modal  *modal.Client     // issue 057
}

// WebhookSecrets holds the shared secrets used to verify inbound
// webhook signatures. One field per source so a leak of one secret
// doesn't compromise another.
//
// Sourced from environment variables at boot (cmd/server) — never from
// a header or a config file. Empty values are accepted at boot but
// the webhook handler refuses every delivery in that state.
type WebhookSecrets struct {
	// GitHub is the value configured in the GitHub webhook UI as
	// "Secret". The X-Hub-Signature-256 header is HMAC-SHA256 of the
	// raw request body keyed by this value.
	GitHub string

	// Linear is the equivalent for Linear webhooks (issue 042). Holds
	// space here so 042 can populate without re-shaping Deps.
	Linear string
}
