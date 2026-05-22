---
type: AFK
depends-on:
  - 044-server-ingestion-consumer
---

## Parent PRD

`ARCHITECTURE.md` §4 + §9 Step 4: "Embedding worker (batch from `embed:queue`, retry with backoff, DLQ after 5 failures)."

## What to build

Long-running worker (in the same Go binary) that drains `embed:queue` and writes embedded vectors to `session_embeddings`.

Loop:

1. `BLPOP embed:queue 5` — block up to 5s. Receives `{ tenant_id, session_id, source_text }`.
2. Batch up to 32 messages or 100ms (whichever first). Voyage's embedding API takes batches; one HTTP call per batch.
3. Call the embedding provider abstraction (Step 3). Use the SHA256(source_text) cache (Step 3) to avoid re-embedding identical prompts.
4. Persist to `session_embeddings`: `INSERT … ON CONFLICT (session_id) DO UPDATE SET embedding = EXCLUDED.embedding, embedding_model = EXCLUDED.embedding_model, created_at = now()`.
5. Per-message retry with exponential backoff (1s, 2s, 4s, 8s, 16s — max 5 attempts). After 5, push to DLQ list `embed:queue:dlq` with the original message + error.

Concurrency: `EMBED_WORKER_COUNT` workers (default 2). Each is a goroutine. They share the same `embed:queue` (Redis list, not stream — at-least-once + simple).

Cost-aware:

- If the embedding provider is rate-limited (HTTP 429), back off the batch and re-queue at the tail of `embed:queue` (NOT the head — fairness across tenants). Log `embed_rate_limited`.
- If the provider is down (circuit breaker open per Step 3), pause the loop (sleep 30s, re-check breaker) — do NOT drain the queue into the DLQ for a transient outage.

## Acceptance criteria

- [ ] Worker count configurable via `EMBED_WORKER_COUNT`
- [ ] Batching verified: 32 messages in one HTTP call (assert via injected provider stub)
- [ ] SHA256 cache hit avoids the network call entirely (test asserts the provider stub is NOT invoked on cache hit)
- [ ] Upsert dedup verified: re-embedding the same session_id replaces the row, not appends
- [ ] Retry schedule verified with a clock-injected test
- [ ] DLQ after 5 attempts; entry preserves the original message + final error
- [ ] Circuit-breaker-open path: worker pauses, does NOT drain to DLQ
- [ ] Graceful shutdown: in-flight batch finishes; queued messages remain in `embed:queue`
- [ ] Integration test against testcontainers Postgres + Redis with a stub provider
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/044-server-ingestion-consumer.md`
- Soft-depends on Step 3 embedding provider abstraction + SHA256 cache

## User stories addressed

Every `iter suggest` ANN lookup depends on this worker keeping `session_embeddings` populated.
