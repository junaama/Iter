// Package app holds process-level wiring shared by cmd/server and the
// internal HTTP/WS handlers — primarily the Deps struct that the boot
// entry point constructs and threads through NewRouter (issue 028).
//
// Deps is intentionally tiny at issue 048: only what cmd/server itself
// needs to boot (logger, build version). Later slices grow it:
//   - issue 049 adds a *pgxpool.Pool (Postgres).
//   - issue 050 added a *redis.Client (Redis Streams + cache).
//   - issue 056 adds an *auth.Verifier (WorkOS JWT verifier).
//   - issue 057 adds a *modal.Client (scoring stub).
//
// Keeping wiring in one struct (rather than passing each dep individually)
// makes it cheap to grow without touching every handler signature.
package app

import (
	"log/slog"

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

	// Redis is the cache + Redis Streams client (issue 050). Optional:
	// some workloads (e.g. a pure migration runner sub-command or a
	// local-only smoke build before REDIS_URL is provisioned) can run
	// without Redis. Components that require it MUST nil-check at use
	// site and return a clear error rather than panicking; cmd/server
	// logs a warning at boot when REDIS_URL is unset.
	Redis *goredis.Client

	// Extension points (deferred):
	//   DB     *pgxpool.Pool     // issue 049
	//   Auth   *auth.Verifier    // issue 056
	//   Modal  *modal.Client     // issue 057
}
