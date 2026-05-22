---
type: AFK
depends-on:
  - 031-auth-middleware-workos-jwt
---

## Parent PRD

`ARCHITECTURE.md` §5 "Versioning + idempotency + rate limits": **100/min CLI, 600/min daemon, per token**. §9 Step 4: "Rate limit middleware (per-token sliding window in Redis)."

## What to build

`internal/api/middleware/ratelimit.go` — token-aware sliding-window limiter backed by Redis.

Design:

1. **Sliding-window log** algorithm (one Redis ZSET per token, score = unix-ms timestamp, value = request id from middleware 014). At each request: `ZADD`, `ZREMRANGEBYSCORE` to evict entries older than 60s, `ZCARD` to count. If count > limit, reject with 429.
2. Limit is derived from the principal's token type (claim `token_type` in the JWT: `cli` or `daemon`). Default 100/min if claim missing or unknown.
3. Atomic Redis pipeline (`MULTI` / `EXEC`) so concurrent requests on the same token can't both pass the check.
4. Response on 429: `Retry-After: <seconds-until-oldest-entry-falls-off>` + body `{"error":"rate_limited","limit":N,"window_seconds":60}`. Per the spec, do NOT leak the deny-list pattern OR the rate-limit values beyond what's necessary for the client to back off.
5. Key prefix: `ratelimit:<token_id>`. Use the JWT `jti` claim (or hash of the raw token if `jti` absent).

## Acceptance criteria

- [ ] Different limits applied based on `token_type` claim (cli=100/min, daemon=600/min)
- [ ] 60s sliding window verified: at second 0 send 100 (CLI), second 30 send 50 — all accepted; second 30 send a 101st in the same window — 429; second 61 the second-30 batch should not count
- [ ] Atomic check + record (`WATCH`/`MULTI` or Lua script) — race test with 200 concurrent goroutines
- [ ] `Retry-After` header is integer seconds until the oldest entry expires
- [ ] Redis unavailable → fail OPEN (allow the request) but log `ratelimit_redis_unavailable`; this is a deliberate choice to keep `iter suggest` working under Redis outage. Record in `DECISIONS.md`.
- [ ] `/health` exempted (registered before the middleware in the chain)
- [ ] Webhooks exempted (per-token doesn't apply; webhook auth is HMAC, not JWT) — verify by registration order
- [ ] Tests cover: under-limit pass, over-limit block, window slide, atomic concurrency, redis-down fail-open
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/031-auth-middleware-workos-jwt.md`

## User stories addressed

Abusive client containment; protects the LLM cost budget; spec'd P99 latency budget assumes bounded request load.
