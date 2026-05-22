---
type: AFK
depends-on:
  - 032-rate-limit-middleware
  - 033-idempotency-middleware
  - 034-tenant-context-middleware
  - 054-embedding-provider-abstraction
---

## Parent PRD

`ARCHITECTURE.md` §2 "Latency budget for `iter suggest` (≤1s P99)" + §5 "`POST /v1/suggest` contract" + §9 Step 4: "Handlers in this order: `POST /v1/suggest` (full pipeline) → …" — this is THE flagship handler.

## What to build

`POST /v1/suggest` — full pipeline. Each step has a stated latency budget; the whole thing must fit under 1s P99.

Pipeline:

1. Decode + validate `SuggestRequest` (from `contracts.py` / `pkg/contracts/suggest.go`). Reject malformed payloads with 400 + `{"error":"validation","details":{...}}`.
2. Embed the user's prompt via the embedding provider abstraction (Step 3). Use the SHA256 Redis cache (Step 3) — cache hit is ~0ms; miss is ~150ms.
3. Embedding nearest-neighbor search over `session_embeddings` for this tenant: HNSW kNN, k=20. Use the cached embedding from step 2. Returns the top candidates' session_ids + similarity scores.
4. Pull `session_scores` rows for those candidates; filter to top-N by composite_score × similarity. N=5 budget for the LLM context.
5. Build the LLM prompt (refinement template — defer the template details to a `templates/suggest.md` file checked into the repo so reviewers can iterate without touching code).
6. Call the LLM via the provider abstraction (Step 3). Routing + circuit breaker + fallback chain are owned by `internal/llm`. ≤500ms budget.
7. Parse the LLM's JSON response (refined_prompt + confidence + rationale). On parse failure → return `no_suggestion_reason: llm_unparseable`.
8. Run the deny-list (`internal/denylist.Contains` from issue 012) over `refined_prompt`. **Hit ⇒ silent suppress** — return `action: suppress`, no `refined_prompt`, no rationale, no signal of which pattern triggered. Log a security event `denylist_hit` with the opaque pattern_id.
9. Call `internal/suggest.SuggestionAction(confidence, refined_prompt)` (issue 009). The function is the single source of truth for threshold decisions.
10. Persist a row in `suggestions` (deduped by `suggestion_hash` of the source_prompt) — `INSERT … ON CONFLICT … DO UPDATE` to bump `hit_count` and `last_used_at`. The write must NOT block the response: fire-and-forget via a goroutine that uses a fresh context (so request cancellation doesn't kill the persist).
11. Build the `SuggestResponse` envelope and return.

Spec for the wire types is in `contracts.py`. Mirror in `pkg/contracts/suggest.go`.

Error mapping:
- LLM all-providers-down → 200 with `action: suppress`, `no_suggestion_reason: llm_unavailable` (per `ARCHITECTURE.md` §7 "LLM provider down" — the handler stays up; clients gracefully degrade)
- No nearby past sessions (k=0) → 200 with `action: suppress`, `no_suggestion_reason: no_evidence`
- All candidates below 0.5 confidence → 200 with `action: suppress`, `no_suggestion_reason: low_confidence`
- Embedding provider down → 503 (we can't proceed without an embedding)
- Postgres down → 503 with `Retry-After: 5`

## Acceptance criteria

- [ ] Endpoint registered at `POST /v1/suggest`; behind auth + rate-limit + idempotency + tenant-context middlewares
- [ ] `Idempotency-Key` required (400 without)
- [ ] Validation rejects malformed bodies with structured error details
- [ ] Embedding cache hit path measured at <5ms; miss path measured at <200ms (table-driven test with injected provider)
- [ ] Deny-list check runs AFTER the LLM response is parsed but BEFORE any persistence; on hit, NOTHING about the matched pattern leaks into the response or the persisted suggestion
- [ ] `no_suggestion_reason` matches one of the documented enum values in `contracts.py` exactly
- [ ] Persistence is fire-and-forget (handler returns before the goroutine completes); test asserts the response latency excludes persist time
- [ ] Integration test (with testcontainers) covers the happy path end-to-end against a real Postgres
- [ ] Latency assertion: at P99 over 100 in-process invocations with mocked providers, handler returns in <50ms (the ≤1s budget is for prod LLM calls; we test the orchestration overhead alone here)
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/032-rate-limit-middleware.md`, `033-idempotency-middleware.md`, `034-tenant-context-middleware.md`
- Blocked by Step 3 storage-layer baseline — embedding provider + LLM provider + suggestions repo

## User stories addressed

The flagship user story: Adam types `iter suggest "..."` and gets a refined prompt within 1s P99.
