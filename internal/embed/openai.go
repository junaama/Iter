package embed

import (
	"context"
	"os"
)

// OpenAIProvider is a stub at issue 054. The interface and breaker
// scaffolding land here; the actual HTTP wiring is a follow-up. Mirrors
// internal/llm's stub pattern exactly so cmd/server's priority chain can
// reference "openai" today and start serving real traffic the moment the
// implementation arrives — no migration to the rest of the codebase.
//
// Default model when wired: text-embedding-3-small (1536-dim) — matches
// the schema column width.
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

// Embed is a stub: returns ErrProviderNotImplemented when a key is set
// (so the router falls through cleanly) or ErrProviderNotConfigured when
// it is not (development environments without all keys).
func (p *OpenAIProvider) Embed(_ context.Context, _ EmbedRequest) (EmbedResponse, error) {
	if p.apiKey == "" {
		return EmbedResponse{}, ErrProviderNotConfigured
	}
	return EmbedResponse{}, ErrProviderNotImplemented
}
