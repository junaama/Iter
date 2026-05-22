package llm

import (
	"context"
	"os"

	"github.com/iter-dev/iter/pkg/contracts"
)

// GoogleProvider is a stub at issue 055. See openai.go for the
// "stub-now, wire-later" rationale.
//
// Models per tier (DECISIONS.md):
//   - CheapHot → gemini-2.5-flash
//   - Sonnet/Opus → NOT SUPPORTED at v1 (the priority chain reserves the
//     reasoning tiers for Anthropic).
type GoogleProvider struct {
	apiKey string
}

// GoogleConfig configures the provider.
type GoogleConfig struct {
	APIKey string
}

// NewGoogleProvider builds a Google provider. Reads GOOGLE_AI_API_KEY from
// env when APIKey is empty.
func NewGoogleProvider(cfg GoogleConfig) *GoogleProvider {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("GOOGLE_AI_API_KEY")
	}
	return &GoogleProvider{apiKey: cfg.APIKey}
}

// Name implements Provider.
func (*GoogleProvider) Name() string { return "google" }

// Supports implements Provider.
func (*GoogleProvider) Supports(tier contracts.LLMTier) bool {
	return tier == contracts.LLMTierCheapHot
}

// Complete is a stub: returns ErrProviderNotImplemented (or
// ErrProviderNotConfigured if no key).
func (p *GoogleProvider) Complete(_ context.Context, _ contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error) {
	if p.apiKey == "" {
		return contracts.LLMCompletionResponse{}, ErrProviderNotConfigured
	}
	return contracts.LLMCompletionResponse{}, ErrProviderNotImplemented
}
