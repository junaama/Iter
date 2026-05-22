---
type: AFK
depends-on:
  - 028-http-router-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 4: "Middleware stack: request ID → logger → auth → tenant context → rate limit → idempotency → handler."

## What to build

The first three middlewares in the stack: request ID, structured logger, panic recover. Auth (016), tenant context (019), rate limit (017), and idempotency (018) land in their own slices and slot into the same stack in the order spec'd above.

1. `internal/api/middleware/request_id.go` — generate a ULID per request, propagate via `context.WithValue` and the `X-Request-ID` response header. Honor inbound `X-Request-ID` if present and well-formed; otherwise mint a new one.
2. `internal/api/middleware/logger.go` — structured `slog` wrapper. Each request emits one line at completion: method, path, status, bytes, duration, request_id, tenant_id (if known), user_id (if known). Failures (5xx) log at `error`; 4xx at `info`; 2xx/3xx at `debug` (toggleable per the `LOG_LEVEL` env). Don't log request bodies — privacy + size.
3. `internal/api/middleware/recover.go` — `defer recover()` around the handler chain. On panic: log full stack at `error`, return 500 with a generic body (`{"error":"internal"}`), include the request id in the response header for correlation.
4. `internal/api/middleware/middleware.go` — `Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler` so the stack is declarative in `router.go`.
5. Wire into `NewRouter`: `Chain(RequestID, Logger, Recover)`.

## Acceptance criteria

- [ ] All three middlewares live under `internal/api/middleware/` with tests using `httptest.NewRecorder`
- [ ] Request ID round-trip: inbound `X-Request-ID` header is preserved; missing one is minted; malformed (too long, non-ASCII) is replaced with a fresh ULID
- [ ] Logger emits exactly one line per request, JSON-formatted, with the fields listed above; assert via captured `slog.Handler`
- [ ] Recover middleware: a handler that panics returns 500, response body is generic, stack trace lands in the log not the response
- [ ] Stack order in `NewRouter` matches the spec line in `ARCHITECTURE.md` §9 Step 4 verbatim (request ID → logger → recover → [auth → tenant → rate limit → idempotency placeholders])
- [ ] 100% line coverage on `internal/api/middleware/`
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/028-http-router-skeleton.md`

## User stories addressed

Underpins every handler — observability, debuggability, and crash containment.
