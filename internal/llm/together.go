package llm

import (
	"context"
	"os"

	"github.com/iter-dev/iter/pkg/contracts"
)

// TogetherProvider is a stub at issue 055. See openai.go for the
// "stub-now, wire-later" rationale.
//
// Models per tier (DECISIONS.md):
//   - CheapHot → Qwen open-weights (hosted on Together)
//   - Sonnet/Opus → NOT SUPPORTED at v1.
type TogetherProvider struct {
	apiKey string
}

// TogetherConfig configures the provider.
type TogetherConfig struct {
	APIKey string
}

// NewTogetherProvider builds a Together provider. Reads TOGETHER_API_KEY
// from env when APIKey is empty.
func NewTogetherProvider(cfg TogetherConfig) *TogetherProvider {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("TOGETHER_API_KEY")
	}
	return &TogetherProvider{apiKey: cfg.APIKey}
}

// Name implements Provider.
func (*TogetherProvider) Name() string { return "together" }

// Supports implements Provider.
func (*TogetherProvider) Supports(tier contracts.LLMTier) bool {
	return tier == contracts.LLMTierCheapHot
}

// Complete is a stub: returns ErrProviderNotImplemented (or
// ErrProviderNotConfigured if no key).
func (p *TogetherProvider) Complete(_ context.Context, _ contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error) {
	if p.apiKey == "" {
		return contracts.LLMCompletionResponse{}, ErrProviderNotConfigured
	}
	return contracts.LLMCompletionResponse{}, ErrProviderNotImplemented
}
