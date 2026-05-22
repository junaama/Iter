package embed

import (
	"context"
	"os"
)

// GoogleProvider is a stub at issue 054. See openai.go for the
// "stub-now, wire-later" rationale. Mirrors internal/llm's stub pattern.
//
// Default model when wired: text-embedding-004 (768-dim) — would need a
// dim-adaptation step (PCA / truncation) before this matches the
// schema's 1536-dim columns; deferred until the SDK lands.
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

// Embed is a stub: returns ErrProviderNotImplemented (or
// ErrProviderNotConfigured if no key).
func (p *GoogleProvider) Embed(_ context.Context, _ EmbedRequest) (EmbedResponse, error) {
	if p.apiKey == "" {
		return EmbedResponse{}, ErrProviderNotConfigured
	}
	return EmbedResponse{}, ErrProviderNotImplemented
}
