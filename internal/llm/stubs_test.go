package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/iter-dev/iter/pkg/contracts"
)

// These exercise the stub providers (openai/google/together) — the actual
// SDK wiring is deferred, so the only behavior to lock down today is the
// "no key → ErrProviderNotConfigured; key set → ErrProviderNotImplemented"
// contract that the router relies on for fall-through.

func TestStubProvidersReturnNotConfiguredWithoutKey(t *testing.T) {
	cases := map[string]Provider{
		"openai":   NewOpenAIProvider(OpenAIConfig{}),
		"google":   NewGoogleProvider(GoogleConfig{}),
		"together": NewTogetherProvider(TogetherConfig{}),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := p.Complete(context.Background(), contracts.LLMCompletionRequest{
				Tier:      contracts.LLMTierCheapHot,
				Messages:  []contracts.LLMMessage{{Role: contracts.LLMRoleUser, Content: "x"}},
				MaxTokens: 8,
			})
			if !errors.Is(err, ErrProviderNotConfigured) {
				t.Errorf("%s without key: got %v, want ErrProviderNotConfigured", name, err)
			}
		})
	}
}

func TestStubProvidersReturnNotImplementedWithKey(t *testing.T) {
	cases := map[string]Provider{
		"openai":   NewOpenAIProvider(OpenAIConfig{APIKey: "x"}),
		"google":   NewGoogleProvider(GoogleConfig{APIKey: "x"}),
		"together": NewTogetherProvider(TogetherConfig{APIKey: "x"}),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := p.Complete(context.Background(), contracts.LLMCompletionRequest{
				Tier:      contracts.LLMTierCheapHot,
				Messages:  []contracts.LLMMessage{{Role: contracts.LLMRoleUser, Content: "x"}},
				MaxTokens: 8,
			})
			if !errors.Is(err, ErrProviderNotImplemented) {
				t.Errorf("%s with key: got %v, want ErrProviderNotImplemented", name, err)
			}
		})
	}
}

func TestStubProviderTierGating(t *testing.T) {
	// OpenAI is configured to support CheapHot + Sonnet but not Opus.
	p := NewOpenAIProvider(OpenAIConfig{})
	if p.Supports(contracts.LLMTierOpus) {
		t.Error("openai stub should NOT advertise Opus support")
	}
	if !p.Supports(contracts.LLMTierCheapHot) {
		t.Error("openai stub should advertise CheapHot support")
	}
	// Google + Together: cheap-only.
	if NewGoogleProvider(GoogleConfig{}).Supports(contracts.LLMTierSonnet) {
		t.Error("google stub should NOT advertise Sonnet support")
	}
	if NewTogetherProvider(TogetherConfig{}).Supports(contracts.LLMTierSonnet) {
		t.Error("together stub should NOT advertise Sonnet support")
	}
}
