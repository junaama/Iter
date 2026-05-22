---
type: AFK
depends-on:
  - 029-middleware-scaffold
  - 020-llm-provider-abstraction
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 4: "`/health` returns ok + db + redis + llm_routes." Sample response shape lives in `deploy.md` §"Healthcheck."

## What to build

`GET /health` — public (NOT behind auth, rate limit, or idempotency middleware). Probes downstream dependencies and returns a JSON envelope. Used by Railway and BetterStack at 30s intervals; latency budget: 500ms hard cap.

Probes:
- **db**: `SELECT 1` against the pgxpool with a 200ms per-probe timeout
- **redis**: `PING` with a 200ms per-probe timeout
- **llm_routes**: per-provider best-effort. Don't actually call the LLM. Inspect the local circuit-breaker state from `internal/llm`. Each entry: `ok | degraded | down`. The endpoint stays up even if every provider is down (suggest endpoint will surface the failure on its own path).

Run all probes concurrently with a single 500ms deadline; return whatever completes. Slow probe → `degraded` for that subsystem.

Response shape (mirror `deploy.md`):

```json
{
  "ok": true,
  "version": "<build_version>",
  "db": "ok",
  "redis": "ok",
  "llm_routes": { "anthropic": "ok", "openai": "ok", "google": "degraded" },
  "uptime_seconds": 3601
}
```

200 if `db` and `redis` are `ok`; 503 otherwise. `llm_routes` are informational only.

## Acceptance criteria

- [ ] `/health` registered OUTSIDE the auth/rate-limit/idempotency middlewares (in front of them in the chain)
- [ ] Concurrent probes with one shared 500ms deadline; latency assertion in the test
- [ ] 200 with `ok=true` when db+redis are reachable; 503 when either is down
- [ ] Provider circuit-breaker state surfaces via `llm_routes`
- [ ] Response shape matches `deploy.md` §"Healthcheck" exactly
- [ ] Tests cover: all-green path, db-down → 503, redis-down → 503, one provider degraded (still 200)
- [ ] Probe timeouts are testable (inject the deadline; don't sleep for real)
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/029-middleware-scaffold.md`
- Blocked by Step 3 storage-layer baseline (`issues/done/020-llm-provider-abstraction.md`) — needs pgxpool, Redis client, LLM provider abstraction

## User stories addressed

On-call / oncall paging: Railway + BetterStack rely on this. First handler users see when something is wrong.
