// Package app holds process-level wiring shared by cmd/server and the
// internal HTTP/WS handlers — primarily the Deps struct that the boot
// entry point constructs and threads through NewRouter (issue 028).
//
// Deps is intentionally tiny at issue 048: only what cmd/server itself
// needs to boot (logger, build version). Later slices grow it:
//   - issue 049 adds *pgxpool.Pool (DB) and BatchDB *pgxpool.Pool.
//   - issue 050 adds a *redis.Client (Redis Streams + cache).
//   - issue 056 adds an *auth.Verifier (WorkOS JWT verifier).
//   - issue 057 adds a *modal.Client (scoring stub).
//
// Keeping wiring in one struct (rather than passing each dep individually)
// makes it cheap to grow without touching every handler signature.
package app

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
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
	// jobs: nightly scoring (issue 046) and archive cron (issue 047).
	// Never reachable from the request path; cmd/server leaves this
	// nil today because the Modal worker (per ARCHITECTURE.md §9
	// Step 4) owns its own connection rather than sharing the server
	// pool. Reserved here so later wiring slices can populate it
	// without re-shaping Deps.
	BatchDB *pgxpool.Pool

	// Extension points (deferred):
	//   Redis  *redis.Client     // issue 050
	//   Auth   *auth.Verifier    // issue 056
	//   Modal  *modal.Client     // issue 057
}
