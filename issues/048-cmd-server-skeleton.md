---
type: AFK
depends-on:
  - 007-go-module-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Repository structure: one Go module, `cmd/server`, `internal/{db,api,ws,ingest,scoring,redact,auth}`, `pkg/contracts`"). See also §9 Step 4 for what `/health` will eventually return — this slice ships the skeleton only.

## What to build

A bootable `cmd/server` binary that exposes a minimal `/health` endpoint, plus the empty package layout Step 3 calls for. This is the spine that 049 (Postgres) and 050 (Redis) layer onto.

Specifically:

1. `cmd/server/main.go` — wires a router, binds `PORT` (default `8080`), graceful shutdown on SIGTERM/SIGINT.
2. **Pick `chi` or `gin` and lock it.** Per §9 Step 4 the choice is fixed for the whole codebase. Record in `DECISIONS.md`. Recommend `chi` (stdlib-aligned, smaller surface, idiomatic middleware).
3. `internal/api` package containing the router constructor + middleware chain stubs in declaration order (request id → logger → auth → tenant context → rate limit → idempotency). Each middleware exists as a pass-through stub; real behavior lands in Step 4.
4. `internal/api.HealthHandler` returning `{"ok": true, "version": "<ldflags>"}` — no DB or Redis check yet (those land in 049/050).
5. Package stubs (`doc.go` only) for `internal/{ws,ingest,auth}` so the §9 Step 3 layout is complete on disk.
6. A `Makefile` `run` target (`go run ./cmd/server`) for local development.
7. Version string injected via `-ldflags "-X main.version=$(git describe --tags --dirty)"`.

Do NOT wire pgxpool, Redis, WorkOS, or any external dependency here — that's the point of subsequent slices.

## Acceptance criteria

- [ ] `cmd/server/main.go` exists; `go run ./cmd/server` boots and serves `/health` on `localhost:8080`
- [ ] Router choice (`chi` or `gin`) recorded in `DECISIONS.md` with one-line rationale
- [ ] Middleware chain stubs present in correct order in `internal/api`
- [ ] `internal/{ws,ingest,auth}` package stubs exist with `doc.go`
- [ ] `/health` returns `200 {"ok": true, "version": "<...>"}`
- [ ] Graceful shutdown: SIGTERM drains in-flight requests with a configurable timeout (default 10s)
- [ ] `make run` target added
- [ ] `make test` + `make lint` pass
- [ ] An `internal/api/api_test.go` exercises `/health` via `httptest.NewServer`

## Blocked by

- Blocked by `issues/007-go-module-skeleton.md`

## User stories addressed

Foundational for every API endpoint, the BetterStack uptime monitor (062), and Railway CD (060).
