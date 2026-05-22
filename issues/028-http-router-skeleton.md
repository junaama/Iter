---
type: HITL
depends-on:
  - 013-cmd-server-skeleton
---

# HITL — router choice is architectural

`ARCHITECTURE.md` §9 Step 4 says "HTTP server skeleton (chi or gin, **lock one**)." That lock is a one-way door for everything that follows: middleware shape, error-mapping conventions, route registration style, testing approach. A human should make the call (or explicitly delegate it back to the AFK worker with a written preference).

## Parent PRD

`ARCHITECTURE.md` §9 Step 4 (preamble) + §5 "Wire formats" / "Versioning + idempotency + rate limits". `CLAUDE.md` "Wire formats" invariant — REST/JSON only.

## What to build

The minimal Go binary that boots an HTTP server and exits cleanly. No handlers yet — just the surface every Step 4 issue plugs into.

1. `cmd/server/main.go` — entry point. Reads `PORT` (default 8080), wires the router from `internal/api.NewRouter`, registers signal handling, runs `http.Server.Shutdown` with a 10s context on `SIGTERM`/`SIGINT`.
2. `internal/api/router.go` — `NewRouter(deps) http.Handler`. No routes wired yet beyond a placeholder so we can probe with curl. `deps` is a struct that later issues will extend (db pool, redis, llm provider, auth verifier, …). Start minimal: `Logger *slog.Logger`, `BuildVersion string`.
3. `internal/api/server.go` — `Server` struct that owns the `http.Server` + the router, plus `Run(ctx)` / `Shutdown(ctx)` methods. Read/write timeouts: 15s. Idle timeout: 60s.
4. `Makefile` — `make run` target (`go run ./cmd/server`).
5. Lock the choice in `DECISIONS.md`. Cover: why chi vs. gin (recommend chi: stdlib-shaped `http.Handler`, no global state, smaller dep graph; gin is fine but uses its own `gin.Context` everywhere). Capture the trade-offs honestly — this is the doc the next maintainer reads.

## Acceptance criteria

- [ ] Router choice recorded in `DECISIONS.md` with one-paragraph rationale
- [ ] `cmd/server/main.go` builds and runs; `make run` prints "listening on :8080"
- [ ] `curl http://localhost:8080/__placeholder` returns 404 (or 200 if you decide to stub `/`)
- [ ] `SIGTERM` triggers graceful shutdown (in-flight requests complete; new ones rejected). Test via `make run &`, `kill -TERM $!`, observe clean exit
- [ ] `internal/api.NewRouter` signature stable enough that issues 014–027 can register routes on the returned `http.Handler` without churn
- [ ] No `panic` paths in the boot sequence — errors return non-zero exit codes with a `slog.Error` line first
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by Step 3 storage-layer baseline (`issues/done/013-cmd-server-skeleton.md`) — needs the dep injection shape settled

## User stories addressed

Foundational; unblocks every handler in Step 4.
