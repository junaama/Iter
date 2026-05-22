package llm

import (
	"context"
	"errors"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Provider is the narrow boundary every concrete LLM client implements. The
// interface is deliberately small: only text completion is needed for
// `iter suggest` and the nightly scorer at v1. Streaming and tool-use are
// deferred to a later slice; adding them here would force every stub
// (openai, google, together) to grow surface area we don't exercise.
//
// Concrete implementations live alongside this file: anthropic.go, openai.go,
// google.go, together.go, stub.go. Each one declares which tiers it serves
// via Supports.
type Provider interface {
	// Name is a stable, lowercase identifier ("anthropic", "openai", ...).
	// Used by Router for log lines and HealthSnapshot keys.
	Name() string

	// Supports reports whether the provider serves the given tier. The
	// router uses this to skip providers that don't speak the requested
	// quality class (e.g. google currently only ships a CheapHot model).
	Supports(tier contracts.LLMTier) bool

	// Complete issues a single text completion. Implementations must honor
	// ctx.Deadline — `iter suggest` has a 1s P99 budget and never blocks on
	// a slow provider beyond what the caller permitted.
	Complete(ctx context.Context, req contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error)
}

// ErrProviderNotConfigured is returned by an implementation that was
// registered without the env var it needs (e.g. ANTHROPIC_API_KEY). The
// router treats this as a normal failure and falls through to the next
// provider in the chain.
var ErrProviderNotConfigured = errors.New("llm: provider not configured")

// ErrProviderNotImplemented is returned by stub implementations whose
// actual SDK wiring is deferred to a later slice. Wired into the breaker
// the same way ErrProviderNotConfigured is.
var ErrProviderNotImplemented = errors.New("llm: provider not implemented")

// ErrAllProvidersUnavailable is the sentinel the router returns when every
// configured provider for the requested tier is either broken (open
// breaker) or has errored on this call. Callers in the suggest path
// pattern-match this to emit `no_suggestion_reason: llm_unavailable`
// (ARCHITECTURE.md §7).
var ErrAllProvidersUnavailable = errors.New("llm: all providers unavailable")
