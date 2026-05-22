---
type: AFK
depends-on:
  - 043-websocket-gateway
  - 051-repositories-tenancy-sessions
---

## Parent PRD

`ARCHITECTURE.md` §3 + §4 + §9 Step 4: "Daemon ingestion pipeline (… server-side Redis Stream → Postgres → embedding enqueue)." This is the SERVER half — the daemon (Mac-side) half lands in Step 8.

## What to build

Server-side ingestion consumer. Subscribes to the WS gateway's parsed `Ingest` messages, writes to Postgres, and enqueues embedding requests on Redis.

Two stages:

### Stage 1: WS → Redis Stream

The WS gateway's `Ingest` handler (registered via 028's router) drops the message onto the durable Redis Stream `ingest:queue` (one stream per tenant, sharded by `tenant_id` to avoid head-of-line blocking). Ack the WS client only AFTER the `XADD` succeeds.

### Stage 2: Stream → Postgres + Embedding queue

A consumer-group worker (group: `ingest-consumers`) reads from the stream and:

1. **Re-classifies via trufflehog** (`internal/redact.Classify` from issue 010). Even though the daemon redacts pre-flight, the spec calls for a server-side defense-in-depth re-scan. `dirty` → drop the message, write `audit_log` entry `leak_detected_post_ingestion`, do NOT persist. Per `ARCHITECTURE.md` §7 "Post-ingestion leak."
2. **Persist** to `sessions` + `session_events` in a single tx with `SET LOCAL app.current_tenant`. Repeated deliveries are deduplicated by `(tenant_id, session_id, event_id)` upserts.
3. **Enqueue embedding** — push the new session_id + redacted_prompt to Redis list `embed:queue` (consumed by issue 030). At-least-once delivery; the embedding worker is idempotent on conflict.
4. **Ack** the stream message via `XACK`; on persistent failure (5+ retries), move to DLQ stream `ingest:queue:dlq:<tenant_id>` with the original error.

Worker is a long-running goroutine in the same Go binary; pool size driven by env var `INGEST_WORKER_COUNT` (default 4).

## Acceptance criteria

- [ ] WS `Ingest` handler `XADD`s to `ingest:queue:<tenant_id>` and acks the client only after success
- [ ] Consumer-group worker reads from the stream; consumer name = hostname + pid for traceability
- [ ] Post-ingestion-leak detection: a `dirty`-classified message is dropped + audit-logged; verify in test with a fixture from `internal/redact/testdata/secrets/`
- [ ] Upsert dedup verified: replaying the same message twice produces one row in `sessions` and one in `session_events`
- [ ] Embedding queue enqueue happens AFTER persist; failure to enqueue logs but does not roll back persist (embedding worker can pick it up from a backfill query)
- [ ] DLQ moves after 5 failures; the original error + stack trace are written to the DLQ entry
- [ ] Graceful shutdown: in-flight messages finish or get re-queued via `XAUTOCLAIM`
- [ ] Integration test against testcontainers Postgres + Redis (the postgres module already wired; add a Redis module)
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/043-websocket-gateway.md`
- Blocked by Step 3 storage-layer baseline — sessions + session_events repository; Redis client

## User stories addressed

The "Adam types prompt → daemon captures → server stores → ready for suggest" pipeline; the core daemon-server contract.
