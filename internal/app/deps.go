// Package app holds process-level wiring shared by cmd/server and the
// internal HTTP/WS handlers — primarily the Deps struct that the boot
// entry point constructs and threads through NewRouter (issue 028).
//
// Deps is intentionally tiny at issue 048: only what cmd/server itself
// needs to boot (logger, build version). Later slices grow it:
//   - issue 049 adds a *pgxpool.Pool (Postgres).
//   - issue 050 adds a *redis.Client (Redis Streams + cache).
//   - issue 056 adds an *auth.Verifier (WorkOS JWT verifier).
//   - issue 057 adds a *modal.Client (scoring stub).
//
// Keeping wiring in one struct (rather than passing each dep individually)
// makes it cheap to grow without touching every handler signature.
package app

import (
	"log/slog"

	"github.com/iter-dev/iter/internal/llm"
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

	// LLM is the multi-provider router (issue 055). May be nil in tests
	// that don't exercise the suggest path; handlers that require it
	// must nil-check and return 503 (mapped to `no_suggestion_reason:
	// llm_unavailable` by the suggest handler, ARCHITECTURE.md §7).
	LLM *llm.Router

	// Extension points (deferred):
	//   DB     *pgxpool.Pool     // issue 049
	//   Redis  *redis.Client     // issue 050
	//   Auth   *auth.Verifier    // issue 056
	//   Modal  *modal.Client     // issue 057
}
