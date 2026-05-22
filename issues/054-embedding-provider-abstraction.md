---
type: AFK
depends-on:
  - 050-redis-client-streams-dlq
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Embedding provider abstraction with circuit breaker + Redis cache by SHA256"); §2 "LLM costs at v1 scale" — embeddings target ~$60/day at scale; §4 "Embedding worker" — batch puller every 30s; §7 failure-mode "Embedding provider unavailable" — queue with backoff, session viewable but not searchable until embedding lands.

## What to build

A provider-agnostic embedding abstraction in `internal/embed` that fronts Voyage and OpenAI (in that priority order) with a Redis SHA256 cache and a per-provider circuit breaker.

Specifically:

1. **Interface**:
   ```go
   type Provider interface {
       Name() string
       Embed(ctx context.Context, input string) ([]float32, error)
       Dim() int // always 1536 at v1; document the contract
   }
   ```
2. **Implementations** (HTTP clients only; no key validation in this slice — keys may be empty for now):
   - `voyage.Provider` — calls Voyage `embed-3` (or whichever Voyage SKU produces 1536-dim vectors).
   - `openai.Provider` — calls OpenAI `text-embedding-3-small` (1536-dim).
   - `stub.Provider` — deterministic test provider that hashes the input into a vector. Used by repository and worker tests so they don't touch the network.
3. **Router**: `embed.Client` wraps an ordered list of providers and a `gobreaker` per provider. `Client.Embed(ctx, input)` tries the first non-broken provider; on a 5xx/timeout it opens the breaker and tries the next. Total deadline derived from `ctx`.
4. **Cache**: `Client.Embed` first computes `cacheKey := "embed:" + sha256(provider_name + input)` (lowercase hex); on hit, returns the cached vector; on miss, calls the provider and `SETEX`s with a 24h TTL. Cache miss/hit counters exported for Step 7 metrics.
5. **Dim check**: returned vector length is asserted against `Provider.Dim()`. A mismatch is a programmer error — log a security event and fall back to the next provider.
6. **Config**: providers are constructed from env (`VOYAGE_API_KEY`, `OPENAI_API_KEY`); if a key is empty, the provider is registered as "permanently broken" and skipped without opening a breaker (so dev environments without all keys still work).
7. **Tests** (unit + Redis testcontainer):
   - Cache hit: call twice with same input → second call returns cached value, provider invoked once.
   - Cache key separation: different providers produce different cache keys.
   - Fallback: stub provider 1 returns an error → router falls through to stub provider 2 → vector returned.
   - Breaker: after N consecutive failures, the failing provider is skipped without being called.
   - Dim mismatch: provider returns a 768-dim vector → fall through, log security event.

## Acceptance criteria

- [ ] `internal/embed.Provider` interface defined
- [ ] `voyage.Provider`, `openai.Provider`, `stub.Provider` implementations
- [ ] `embed.Client` with ordered fallback + per-provider `gobreaker`
- [ ] Redis cache keyed by `sha256(provider + input)`, 24h TTL
- [ ] Dim assertion on every response; mismatch falls through
- [ ] Empty API key → provider permanently skipped (no breaker churn in dev)
- [ ] Cache hit/miss counters exposed (Prometheus metric naming defined; emission is Step 7)
- [ ] Tests cover: cache hit, fallback, breaker, dim mismatch, key separation
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/050-redis-client-streams-dlq.md`

## User stories addressed

Every embedding write — the `embed:queue` worker (Step 4), every `POST /v1/suggest` that doesn't cache-hit on `suggestions.SearchSimilar`, the daemon ingestion pipeline.
