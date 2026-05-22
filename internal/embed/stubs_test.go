package embed

import (
	"context"
	"errors"
	"testing"
)

// These exercise the stub providers (openai, google) — the actual HTTP
// wiring is deferred, so the only behavior to lock down today is the
// "no key → ErrProviderNotConfigured; key set → ErrProviderNotImplemented"
// contract that the router relies on for fall-through.

func TestStubProvidersReturnNotConfiguredWithoutKey(t *testing.T) {
	cases := map[string]Provider{
		"openai": NewOpenAIProvider(OpenAIConfig{}),
		"google": NewGoogleProvider(GoogleConfig{}),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := p.Embed(context.Background(), EmbedRequest{Inputs: []string{"x"}})
			if !errors.Is(err, ErrProviderNotConfigured) {
				t.Errorf("%s without key: got %v, want ErrProviderNotConfigured", name, err)
			}
		})
	}
}

func TestStubProvidersReturnNotImplementedWithKey(t *testing.T) {
	cases := map[string]Provider{
		"openai": NewOpenAIProvider(OpenAIConfig{APIKey: "x"}),
		"google": NewGoogleProvider(GoogleConfig{APIKey: "x"}),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := p.Embed(context.Background(), EmbedRequest{Inputs: []string{"x"}})
			if !errors.Is(err, ErrProviderNotImplemented) {
				t.Errorf("%s with key: got %v, want ErrProviderNotImplemented", name, err)
			}
		})
	}
}
