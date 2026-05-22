---
type: AFK
depends-on:
  - 048-cmd-server-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("LLM provider abstraction with routing + circuit breaker + fallback chain"); §2 "LLM costs at v1 scale" — multi-provider routing in place so the cheapest provider per workload wins; §7 failure-mode "LLM provider unavailable" — `no_suggestion_reason: llm_unavailable`, never blocks.

## What to build

A provider-agnostic LLM abstraction in `internal/llm` that fronts Anthropic, OpenAI, Google, and Together (Qwen) with per-provider circuit breakers and a configurable routing strategy.

Specifically:

1. **Interface**:
   ```go
   type Provider interface {
       Name() string
       Tier() Tier // CheapHot | Sonnet | Opus — locked enum
       Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
   }
   ```
   `CompletionRequest` / `CompletionResponse` are wire-type-shaped in `pkg/contracts/llm.go` so they can be re-used by `iter suggest`, scoring, and webhook handlers without import cycles.
2. **Implementations** (HTTP clients; keys may be empty in dev):
   - `anthropic.Provider` (Haiku for `CheapHot`, Sonnet for `Sonnet`, Opus for `Opus`)
   - `openai.Provider` (gpt-4o-mini for `CheapHot`, gpt-4o for `Sonnet`)
   - `google.Provider` (Gemini Flash for `CheapHot`)
   - `together.Provider` (Qwen for `CheapHot`, open-weights)
   - `stub.Provider` — deterministic test provider; returns canned responses keyed by input prefix.
3. **Router**: `llm.Router` accepts a `Tier` + `CompletionRequest` and selects providers in priority order configured per tier. On provider error, falls through; opens `gobreaker` after N consecutive failures. Total deadline derived from `ctx` — never exceeds the caller's budget.
4. **No-suggestion path**: when every provider is broken/errors, return a sentinel error `llm.ErrAllProvidersUnavailable`. Callers (the suggest handler in Step 4) map this to `no_suggestion_reason: llm_unavailable`. The abstraction itself NEVER blocks waiting for retries beyond `ctx`.
5. **Cost accounting** (lightweight at v1): each response carries `tokens_in`, `tokens_out`, and `cost_usd_estimate` (provider-specific computation). Logged for Step 7 Langfuse integration; no DB writes here.
6. **`/health` extension**: optional shallow probe per provider — `llm_routes: {anthropic: ok, ...}` in the response. Probes only fire if `?deep=1` query param is set (avoid pinging providers every 30s).
7. **Tests** (unit only — no live provider calls in this slice):
   - Routing: cheap-tier request routes to providers configured for that tier; sonnet-tier skips cheap-only providers.
   - Fallback: first provider returns 5xx → router falls through; verifies via stub.
   - Breaker: N consecutive failures → provider skipped on next call without being invoked.
   - Deadline: `ctx.Deadline()` shorter than first provider's typical latency → returns `ErrAllProvidersUnavailable` rather than partial success.
   - Sentinel: all-broken state returns `ErrAllProvidersUnavailable`.

## Acceptance criteria

- [ ] `internal/llm.Provider` interface + `Tier` enum (`CheapHot` / `Sonnet` / `Opus`)
- [ ] Anthropic, OpenAI, Google, Together, Stub implementations
- [ ] `llm.Router` with per-tier priority, per-provider `gobreaker`, fallback chain
- [ ] `llm.ErrAllProvidersUnavailable` sentinel; callers can pattern-match it
- [ ] `pkg/contracts/llm.go` carries `CompletionRequest` / `CompletionResponse`
- [ ] `/health?deep=1` returns per-provider status; default `/health` does NOT probe providers
- [ ] Cost-accounting fields on `CompletionResponse` (`tokens_in`, `tokens_out`, `cost_usd_estimate`)
- [ ] Tests cover: routing, fallback, breaker, deadline, sentinel
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/048-cmd-server-skeleton.md`

## User stories addressed

`POST /v1/suggest` (the latency-critical synchronous path), the nightly Modal scoring batch (Modal calls this router via the binary), webhook-triggered classification (if any). The fallback chain is the §7 "LLM provider unavailable" mitigation.
