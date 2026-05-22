// Package embed provides the multi-provider embedding abstraction described
// in ARCHITECTURE.md §9 Step 3 ("Embedding provider abstraction with circuit
// breaker + Redis cache by SHA256"). Callers — the embedding worker, the
// suggest path on cache miss, and the daemon ingestion pipeline — talk to
// *Router, which fronts per-provider implementations behind a per-provider
// circuit breaker and a SHA256-keyed Redis cache.
//
// Why a separate package from internal/llm: embeddings and LLM completions
// are different domains. The wire shapes differ (a batch of strings →
// matrix of float32 vectors vs. a chat-style turn → text), the providers
// differ (Voyage is embedding-only at v1), the cache strategy differs (a
// 1536×float32 vector keyed by SHA256 of model+input vs. no cache at all
// for completions today), and the priority chain differs. Sharing breaker
// state across two unrelated dependency chains would couple their health,
// so the breaker is duplicated here rather than imported.
package embed

import (
	"context"
	"errors"
)

// Provider is the narrow boundary every concrete embedding client
// implements. The interface is batched at the API level: one call returns
// N vectors for N inputs, matching how Voyage / OpenAI / Google all expose
// their embeddings endpoints. Single-input callers pass a one-element
// slice and read Vectors[0].
//
// Concrete implementations live alongside this file: voyage.go (the v1
// real provider), openai.go, google.go (stubs that mirror internal/llm's
// stub pattern — registered today so cmd/server's priority chain doesn't
// rewrite when SDK wiring lands).
type Provider interface {
	// Name is a stable, lowercase identifier ("voyage", "openai",
	// "google"). Used by Router for log lines and HealthSnapshot keys.
	Name() string

	// Embed issues a single batched embedding request. Implementations
	// must honor ctx.Deadline — the suggest path has a ≤1s P99 budget
	// (CLAUDE.md "Locked invariants").
	Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
}

// EmbedRequest is the wire-agnostic batched embed input. Model is
// optional: a provider applies its own default when empty (Voyage defaults
// to voyage-code-3, which yields the 1536-dim vectors that match the
// session_embeddings.embedding vector(1536) column in 0001_initial.sql).
type EmbedRequest struct {
	// Model selects the concrete provider model id. Empty string means
	// "provider default" — the canonical 1536-dim model.
	Model string
	// Inputs is the batch of strings to embed. Order is preserved in the
	// response Vectors slice.
	Inputs []string
}

// EmbedResponse is the wire-agnostic batched embed output. Vectors[i]
// corresponds to EmbedRequest.Inputs[i]. TokensIn is the provider-reported
// input token count for cost accounting (ARCHITECTURE.md §2 LLM costs).
type EmbedResponse struct {
	Vectors  [][]float32
	TokensIn int
}

// ErrProviderNotConfigured is returned by an implementation that was
// registered without the env var it needs (e.g. VOYAGE_API_KEY). The
// router treats this as a normal failure and falls through to the next
// provider in the chain.
var ErrProviderNotConfigured = errors.New("embed: provider not configured")

// ErrProviderNotImplemented is returned by stub implementations whose
// actual HTTP wiring is deferred to a later slice. Wired into the
// breaker the same way ErrProviderNotConfigured is.
var ErrProviderNotImplemented = errors.New("embed: provider not implemented")

// ErrAllProvidersUnavailable is the sentinel the router returns when every
// configured provider is either broken (open breaker) or has errored on
// this call. Callers in the embedding worker / suggest path pattern-match
// this with errors.Is to surface ARCHITECTURE.md §7 "Embedding provider
// unavailable" — queue with backoff, session viewable but not searchable
// until embedding lands.
var ErrAllProvidersUnavailable = errors.New("embed: all providers unavailable")

// ErrRateLimited is returned by providers when the upstream responds with a
// rate-limit signal (HTTP 429). The embedding worker treats it differently
// from ordinary provider failures: the whole batch is re-queued at the tail
// for tenant fairness, without burning per-message retry attempts.
var ErrRateLimited = errors.New("embed: provider rate limited")
