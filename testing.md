# Iter — Testing

This document describes the test suite for Iter v1: what it covers, what it doesn't, and how to run it.

## Single command to run everything

```bash
make test
```

This runs (in order): unit tests → linter → integration tests against a testcontainer Postgres + Redis → security tests. Fails fast on the first failure.

For just unit tests during development:

```bash
make test-unit
```

For e2e tests against staging:

```bash
make test-e2e
```

## What the suite covers

### Unit tests (`go test ./internal/...`)
Pure functions only. No I/O, no goroutines, no clocks. Includes:

- **Scoring**: `compute_composite_score` boundary tests, signal-weighting tests, property tests (output always in [0,1], contributor_weight=0 zeros the score).
- **Suggestion decision**: `suggestion_action` thresholds at 0.499, 0.5, 0.799, 0.8; nil refined_prompt always suppresses.
- **Redaction**: known-secrets corpus must classify as strippable or dirty; idempotent redaction; deterministic output.
- **Signal aggregation**: event-stream ordering independence, idempotency on duplicate event IDs.
- **Deny-list filter**: case-insensitive, substring-matching, URL-decoded inputs all blocked.
- **Auth claim extraction**: well-formed and malformed JWT inputs.

Coverage target: 80% line coverage on `internal/`. Not a CI gate; a signal.

### Integration tests (`go test -tags=integration ./...`)
Real Postgres and Redis via testcontainers. Covers:

- **Repository layer**: every Insert/Get/List/Upsert function against a real DB.
- **Cascade deletes**: parent session → child session → events → embeddings → scores all removed.
- **Row-Level Security**: tenant A cannot see tenant B's data across every tenant-scoped table.
- **WebSocket gateway**: real WS client → server round-trip + ack; reconnect replay; heartbeat ping/pong.
- **Embedding worker**: enqueue → consume → write → verify row.
- **LLM provider routing**: mocked provider responses, verify fallback chain triggers correctly.
- **Idempotency middleware**: same Idempotency-Key returns cached response.
- **Rate limit middleware**: sliding window respects per-token budget.

### End-to-end tests (`go test -tags=e2e ./test/e2e`)
Run against staging. Hits real Railway services, real WorkOS, real LLM providers (in a test tenant). Covers:

- **Login flow**: `iter login` → WorkOS device code → token in mock keychain.
- **Ingest flow**: drop a sample session file → record appears in test DB within 30s.
- **Suggest flow**: known seed corpus + known prompt → expected refined prompt returned.
- **Webhook flow**: post to staging webhook endpoint → outcome row appears, linked to correct session.
- **Archive flow**: 91-day-old session moves to R2 (S3-compatible client → `$R2_ENDPOINT`), archive_pointer row created with `object_uri = r2://...`, original deleted. Test fixture stubs the Cloudflare Analytics API to return 50% usage so the free-tier guardrail does not block writes.
- **The full Adam journey**: sign up → ingest → first suggestion → use refined prompt → see in dashboard. Single test script.

### Load tests (`make load-test`)
Not run on every commit. Run before any release that touches the gateway, suggest path, or scoring batch.

- **WS gateway**: 5K concurrent connections, 1 event/sec each, 10 min. Zero dropped, P99 ack < 100ms.
- **`iter suggest`**: 50 QPS for 30 min. P99 < 1000ms.
- **Embedding worker**: 10K jobs queued at once. All processed within 30 min.

### Security tests (`make test-security`)
Run on every PR.

- Cross-tenant access attempts: 100% blocked by RLS.
- Token replay across tenants: blocked.
- Deny-list bypass attempts (URL-encoded, case-variant, whitespace-padded, multi-line): all blocked.
- Trufflehog regression fixtures: known-leaky inputs always classify as strippable or dirty.
- Webhook signature replay: blocked by idempotency.

## What the suite does NOT cover

These are real gaps. Acknowledged, deferred.

- **Mac app UI testing**: no XCUITest or snapshot tests at v1. Manual smoke before release.
- **Real LLM provider integration tests**: integration tests use mocked providers. Cost and flakiness are too high to hit real APIs on every PR. Real-provider tests run in the e2e suite only.
- **Real-time chaos tests**: no random network partition / pod kill in v1. Add when first incident demonstrates the need.
- **Browser / cross-platform**: Iter is macOS-only at v1. No browser harness tests.
- **Long-running soak tests**: no 7-day uptime test. Trust BetterStack to surface real-world drift.
- **Penetration testing**: deferred to post-revenue, triggered by SOC 2 process.
- **Multi-region failover**: single-region at v1. Not tested.

## CI gates

A PR cannot merge to `main` until:

1. `make test` passes.
2. `golangci-lint run` passes.
3. At least one human review.
4. PR description includes a "what changed" and "how to test" section.

A release to production requires:

1. CI green on `main`.
2. Staging deploy successful for 1h with no new error spikes.
3. Manual smoke of: login, ingest, suggest, dashboard load.
4. Load test if any of: gateway / suggest path / scoring batch were modified.

## Test data

- **Seed fixtures**: `test/fixtures/sessions/` contains representative session JSONL files per harness. Used in integration + e2e.
- **Secrets corpus**: `test/fixtures/secrets/` contains known-leaky inputs. Never run in production paths.
- **PII corpus**: `test/fixtures/pii/` contains synthetic emails, phone numbers, names. Used to test redaction.
- **Embedding seed**: 10K random `vector(1536)` rows seeded into the test DB for ANN tests.

All fixtures are committed to the repo and are synthetic. No real user data is used in tests.
