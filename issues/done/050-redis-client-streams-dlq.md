---
type: AFK
depends-on:
  - 048-cmd-server-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Redis client + stream consumer groups + DLQ naming convention"). `DECISIONS.md` Phase 4: "Durable queue: Redis Streams. No Kafka, no NATS, no SQS." See also §7 failure-mode table — Redis Streams is the 24h-TTL backstop when Postgres is unavailable, and the `dlq:*` namespace is the dead-letter pattern.

## What to build

A Redis client wrapper and Streams helpers in `internal/queue` that the ingestion, embedding worker, scoring-trigger, and webhook-replay paths all share. Plus the `/health` endpoint gets a Redis probe.

Specifically:

1. **Client construction**: `internal/queue.NewClient(ctx, cfg) (*redis.Client, error)` reading `REDIS_URL`. Default `MaxRetries=3`, `DialTimeout=2s`, `ReadTimeout=1s`, `WriteTimeout=1s`, `PoolSize=10`.
2. **Stream helpers**:
   - `queue.Publish(ctx, stream, msg) (id string, err error)` — wraps `XADD`, returns the stream message id.
   - `queue.Consume(ctx, stream, group, consumer string, batch int, handler Handler) error` — wraps `XREADGROUP`; `handler` returns `(ack bool, retry bool)`. On `ack=true` the message is `XACK`ed; on `retry=true` it's left pending; on neither, it's moved to `dlq:<stream>`.
   - `queue.EnsureGroup(ctx, stream, group string) error` — idempotent `XGROUP CREATE ... MKSTREAM`.
3. **DLQ naming convention**: `dlq:<stream-name>`. Document in `internal/queue/doc.go` and in `DECISIONS.md` if not already there. DLQ entries carry the original `messageID`, the original payload, the consumer name, the error string, and an attempt count.
4. **Health check**: extend `/health` (from 048/049) to add `"redis": "ok" | "down"` (PING). Status 503 when Redis is down.
5. **Testcontainers integration test**: spin a `redis:7-alpine` container, exercise publish/consume round-trip, deliberately poison a message and verify it lands in `dlq:<stream>` with the expected envelope.
6. **TTL backstop helper**: `queue.PublishWithTTL(ctx, stream, msg, ttl)` — wraps `XADD` + a follow-up cleanup task. Used for the 24h capture buffer per §7 Postgres-unavailable mitigation.

Stream names used elsewhere in the codebase land in their owning packages — this slice ships the helpers, not the producers/consumers.

## Acceptance criteria

- [ ] `internal/queue.NewClient` exists with documented config
- [ ] `Publish` / `Consume` / `EnsureGroup` / `PublishWithTTL` exist with documented signatures
- [ ] DLQ naming convention `dlq:<stream>` documented; DLQ envelope shape (`originalID`, `payload`, `consumer`, `err`, `attempts`) defined
- [ ] `/health` returns `redis: ok` / 200 when Redis reachable, `redis: down` / 503 otherwise
- [ ] Testcontainers test verifies: round-trip publish/consume, poison-message lands in `dlq:<stream>` with correct envelope, consumer-group idempotent re-create
- [ ] At-least-once delivery semantics documented (consumer must handle duplicate messages)
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/048-cmd-server-skeleton.md`

## User stories addressed

Underpins the ingestion → embedding → scoring queue chain in Step 4, the Postgres-unavailable failure-mode mitigation, and the webhook idempotency replay path.
