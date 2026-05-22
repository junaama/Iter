---
type: AFK
depends-on:
  - 031-auth-middleware-workos-jwt
  - 051-repositories-tenancy-sessions
---

## Parent PRD

`ARCHITECTURE.md` §4 "Topology" + §9 Step 4: "WebSocket gateway: discriminated message router, ack every message, per-connection goroutines, heartbeat ping 30s." `contracts.py` discriminated unions for `ClientMessage` / `ServerMessage`.

## What to build

`GET /v1/ws` — upgrades to WebSocket. Daemon ↔ cloud transport. Server speaks the discriminated message protocol from `contracts.py`.

Components:

1. **Upgrade + auth** — WorkOS JWT validated via the auth middleware (016) BEFORE the upgrade. Reject unauthenticated upgrades with 401. After upgrade, the connection carries the same `Principal` context for its lifetime.
2. **Per-connection goroutines** — one for reads, one for writes, one for heartbeats. Single ownership of the conn: only the writer goroutine touches `conn.WriteMessage`. Reader sends parsed messages onto an unbuffered channel; writer reads from one and from the heartbeat channel.
3. **Discriminated router** — parse `ClientMessage` JSON; dispatch to per-type handlers. Each handler is `func(ctx, principal, msg) (ack, err)`. Unknown discriminator → close with code 1003 + reason "unknown message type" + audit log.
4. **Ack every message** — every `ClientMessage` produces an `Ack` `ServerMessage` referencing the client's `msg_id`. If a handler returns an error, the ack carries `status: error` + a stable error code (no stack traces, no SQL).
5. **Heartbeat** — server sends `Ping` every 30s; expects `Pong` within 10s; if missing, close with code 1011 + reason "heartbeat timeout."
6. **Backpressure** — bounded writer channel (capacity 64). If full, drop the oldest queued non-ack message and log `ws_backpressure_drop`. Acks are never dropped.
7. **Close handling** — on any error, close cleanly with a typed close code; log + audit if security-relevant.

The actual message types (`Ingest`, `RequestSuggest`, `Subscribe`, etc.) are added by 029 (ingestion consumer) and later issues. This issue ships the gateway and one no-op `Ping`/`Pong` handler.

## Acceptance criteria

- [ ] WS upgrade requires a valid WorkOS JWT in the `Sec-WebSocket-Protocol` header (or `Authorization`, whichever pattern wins — record in DECISIONS.md)
- [ ] Per-connection goroutine model verified by race-detector test (`go test -race`)
- [ ] Heartbeat closes the connection on missing pong
- [ ] Unknown discriminator closes cleanly (1003) and audit-logs
- [ ] Backpressure drop is verified with a 100k-msg flood test; acks survive
- [ ] No goroutine leaks under repeated connect/disconnect (`runtime.NumGoroutine` baseline check)
- [ ] Discriminated router is generic enough that 029+ can register message handlers without modifying gateway.go
- [ ] Tests use a local httptest server + a real WebSocket client (`nhooyr.io/websocket` or `coder/websocket`) — pick and pin
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/031-auth-middleware-workos-jwt.md`
- Blocked by Step 3 storage-layer baseline — Principal type + audit_log writer

## User stories addressed

The daemon's only transport to the cloud — every Adam-trace upload flows through this gateway.
