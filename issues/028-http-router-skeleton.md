---
type: AFK
depends-on:
  - 013-cmd-server-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 4 (preamble) + §5 "Wire formats" / "Versioning + idempotency + rate limits". `CLAUDE.md` "Wire formats" invariant — REST/JSON only.

## Locked decision

**Router: `github.com/go-chi/chi/v5`.** Recorded in `DECISIONS.md` under "Implementation decisions"; do NOT reopen unless an architectural reason forces it.

Why chi (in one line): stdlib-shaped `http.Handler` semantics keep middleware composable and reusable, the dep graph is small (no transitive web framework), and the existing middleware sketches in issue 029 assume `func(http.Handler) http.Handler` already.

## What to build

The minimal Go binary that boots an HTTP server and exits cleanly. No handlers yet — just the surface every Step 4 issue plugs into.

1. `cmd/server/main.go` — entry point. Reads `PORT` (default 8080), wires the router from `internal/api.NewRouter`, registers signal handling, runs `http.Server.Shutdown` with a 10s context on `SIGTERM`/`SIGINT`.
2. `internal/api/router.go` — `NewRouter(deps) http.Handler`. Uses chi's `chi.NewRouter()` internally. No routes wired yet beyond a placeholder so we can probe with curl. `deps` is a struct that later issues will extend (db pool, redis, llm provider, auth verifier, …). Start minimal: `Logger *slog.Logger`, `BuildVersion string`.
3. `internal/api/server.go` — `Server` struct that owns the `http.Server` + the router, plus `Run(ctx)` / `Shutdown(ctx)` methods. Read/write timeouts: 15s. Idle timeout: 60s.
4. `Makefile` — `make run` target (`go run ./cmd/server`).
5. Add `github.com/go-chi/chi/v5` to `go.mod`; pin the major version explicitly. Do NOT pull in `chi/middleware` — the middleware stack lands in issue 029, written against `http.Handler` directly so it remains router-agnostic.

## Acceptance criteria

- [ ] `cmd/server/main.go` builds and runs; `make run` prints "listening on :8080"
- [ ] `curl http://localhost:8080/__placeholder` returns 404
- [ ] `SIGTERM` triggers graceful shutdown (in-flight requests complete; new ones rejected). Test via `make run &`, `kill -TERM $!`, observe clean exit
- [ ] `internal/api.NewRouter` signature stable enough that issues 029–047 can register routes on the returned `http.Handler` without churn — verify by sketching the signature in a comment
- [ ] No `panic` paths in the boot sequence — errors return non-zero exit codes with a `slog.Error` line first
- [ ] chi pulled in at the latest v5 release; `go mod tidy` clean
- [ ] No use of `chi/middleware` — keep middleware concerns in issue 029
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/013-cmd-server-skeleton.md` (needs the cmd/server + dep-injection shape settled)

## User stories addressed

Foundational; unblocks every handler in Step 4.
