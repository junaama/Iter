// Package redis owns the Redis client construction and the thin Streams +
// DLQ helpers that the ingestion processor, embedding worker, scoring-
// trigger, and webhook-replay paths share.
//
// Per DECISIONS.md Phase 4 the durable queue at v1 is Redis Streams — no
// Kafka, no NATS, no SQS. Per Phase 7 the dead-letter convention is the
// `dlq:*` namespace; this package's DLQName helper is the only place that
// computes those names so the convention stays single-sourced.
//
// Scope:
//   - client.go — NewClient(ctx, cfg) wrapping github.com/redis/go-redis/v9
//     with Iter-shaped defaults (short timeouts, modest pool size).
//   - streams.go — EnsureStreamAndGroup / ReadGroup / Ack / Claim. These
//     are thin wrappers over XGROUP CREATE / XREADGROUP / XACK / XCLAIM,
//     not a full consumer framework. The owning packages (issue 044/045
//     ingestion + embedding workers) build their consumer loops on top.
//   - dlq.go — DLQName / PushDLQ / ListDLQ. Push envelopes carry the
//     original stream id, payload fields, consumer name, and error string
//     so ops can XRANGE dlq:<stream> and reconstruct what failed.
//
// What this slice does NOT ship:
//   - A `/health` Redis probe — that lands in issue 030 alongside the rest
//     of the health body once issue 028 wires the chi router.
//   - A high-level Publish/Consume framework with retry-and-DLQ semantics
//     — the issue brief describes those, but the owning consumers (044/045)
//     own retry policy, so this slice exposes the primitives only.
//   - PublishWithTTL for the 24h Postgres-unavailable backstop — the
//     producer side of that backstop lives with the ingestion consumer
//     (issue 044) where the fallback decision is made.
//
// At-least-once delivery: XREADGROUP + XACK is at-least-once by design
// (a crash between handler success and XACK redelivers). Consumers must
// be idempotent. The dedup key documented in DECISIONS.md "Dedup key for
// replay idempotency" (issue 011) is the contract.
package redis
