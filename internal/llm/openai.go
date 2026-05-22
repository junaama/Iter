package llm

import (
	"context"
	"os"

	"github.com/iter-dev/iter/pkg/contracts"
)

// OpenAIProvider is a stub at issue 055. The interface and breaker
// scaffolding land here; the actual SDK wiring is a follow-up. Including
// the stub means the router config in cmd/server can reference
// "openai" today and start serving real traffic the moment the
// implementation arrives — no migration to the rest of the codebase.
//
// Models per tier (DECISIONS.md):
//   - CheapHot → gpt-4o-mini
//   - Sonnet   → gpt-4o
//   - Opus     → NOT SUPPORTED (OpenAI does not currently offer an opus-class
//     reasoning model in our priority chain; chain falls through to
//     Anthropic).
type OpenAIProvider struct {
	apiKey string
}

// OpenAIConfig configures the provider.
type OpenAIConfig struct {
	APIKey string
}

// NewOpenAIProvider builds an OpenAI provider. Reads OPENAI_API_KEY from
// env when APIKey is empty.
func NewOpenAIProvider(cfg OpenAIConfig) *OpenAIProvider {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("OPENAI_API_KEY")
	}
	return &OpenAIProvider{apiKey: cfg.APIKey}
}

// Name implements Provider.
func (*OpenAIProvider) Name() string { return "openai" }

// Supports implements Provider.
func (*OpenAIProvider) Supports(tier contracts.LLMTier) bool {
	switch tier {
	case contracts.LLMTierCheapHot, contracts.LLMTierSonnet:
		return true
	default:
		return false
	}
}

// Complete is a stub: returns ErrProviderNotImplemented (or
// ErrProviderNotConfigured if no key) so the router falls through.
func (p *OpenAIProvider) Complete(_ context.Context, _ contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error) {
	if p.apiKey == "" {
		return contracts.LLMCompletionResponse{}, ErrProviderNotConfigured
	}
	return contracts.LLMCompletionResponse{}, ErrProviderNotImplemented
}
