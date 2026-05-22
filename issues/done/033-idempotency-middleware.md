---
type: AFK
depends-on:
  - 029-middleware-scaffold
  - 050-redis-client-streams-dlq
---

## Parent PRD

`CLAUDE.md` "Locked invariants" — **Idempotency-Key required on all POST endpoints**. `ARCHITECTURE.md` §5 "Versioning + idempotency + rate limits" + §9 Step 4: "Idempotency middleware (24h cached responses)."

## What to build

`internal/api/middleware/idempotency.go` — for any POST request, look up the `Idempotency-Key` header in Redis. If a response is cached for that key + endpoint + tenant_id, return it verbatim (status code, headers, body). If not cached, run the handler, persist the result for 24h, then return.

Concretely:

1. **Required for POST** — 400 with `{"error":"missing_idempotency_key"}` if a POST arrives without the header. GET / HEAD / OPTIONS / etc. skip the middleware entirely. (Webhooks: HMAC bodies carry their own idempotency keys per `ARCHITECTURE.md` §5; webhook handlers should set the header themselves before reaching the middleware OR the webhook handler skips this middleware — pick one and document.)
2. **Scoped key** — Redis key = `idempotency:<tenant_id>:<endpoint>:<key>`. Cross-tenant collisions impossible.
3. **In-flight lock** — first request acquires a Redis `SET NX` lock with 60s TTL. Concurrent retries with the same key wait up to 30s on a pubsub channel for completion, then read the cached response. If lock holder times out, second request takes over the lock.
4. **Cache** — serialize the response as `{status, headers, body_b64}` JSON, `SET` with 24h TTL.
5. **Replay header** — set `X-Idempotent-Replay: true` on cached responses so clients can tell.
6. **Body size cap** — refuse to cache responses > 1 MiB; log and return uncached (next replay will re-run). Configurable.

## Acceptance criteria

- [ ] POST without `Idempotency-Key` → 400; GET/PUT/DELETE/PATCH not affected
- [ ] First POST runs the handler; second POST with the same key returns the cached response and `X-Idempotent-Replay: true`
- [ ] Cache key includes tenant_id (cross-tenant safe — verify in test)
- [ ] In-flight lock: 200 concurrent requests with the same key produce ONE handler invocation; the other 199 receive the same response
- [ ] Lock TTL: if the holder crashes, second request takes over after 60s
- [ ] 24h TTL on cached responses — verify with a controllable clock or by reading the Redis TTL
- [ ] Webhook endpoints decision (skip vs. self-populate) recorded in `DECISIONS.md`
- [ ] Tests cover all of the above plus: response > 1 MiB falls through uncached, Redis unavailable → falls through uncached (fail-open) with a log line
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/029-middleware-scaffold.md`
- Blocked by Step 3 storage-layer baseline — Redis client + pubsub abstraction

## User stories addressed

Every POST endpoint's safe-to-retry guarantee — the daemon, CLI, and webhook replay paths all assume this.
