package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iter-dev/iter/pkg/contracts"
)

func TestAnthropicUnconfiguredReturnsSentinel(t *testing.T) {
	p := NewAnthropicProvider(AnthropicConfig{APIKey: ""})
	_, err := p.Complete(context.Background(), contracts.LLMCompletionRequest{
		Tier:      contracts.LLMTierCheapHot,
		Messages:  []contracts.LLMMessage{{Role: contracts.LLMRoleUser, Content: "x"}},
		MaxTokens: 8,
	})
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("got %v, want ErrProviderNotConfigured", err)
	}
}

func TestAnthropicHTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("missing/incorrect X-Api-Key header: %q", r.Header.Get("X-Api-Key"))
		}
		if r.Header.Get("Anthropic-Version") == "" {
			t.Error("missing Anthropic-Version header")
		}

		body, _ := io.ReadAll(r.Body)
		var got anthropicRequest
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		if got.System != "be helpful" {
			t.Errorf("system = %q, want 'be helpful'", got.System)
		}
		if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
			t.Errorf("messages = %+v, want one user message", got.Messages)
		}
		if !strings.Contains(got.Model, "haiku") {
			t.Errorf("model = %q, want a haiku tier", got.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]string{{"type": "text", "text": "hello back"}},
			"model":       got.Model,
			"stop_reason": "end_turn",
			"usage":       map[string]int{"input_tokens": 12, "output_tokens": 34},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", BaseURL: srv.URL})
	resp, err := p.Complete(context.Background(), contracts.LLMCompletionRequest{
		Tier: contracts.LLMTierCheapHot,
		Messages: []contracts.LLMMessage{
			{Role: contracts.LLMRoleSystem, Content: "be helpful"},
			{Role: contracts.LLMRoleUser, Content: "hi"},
		},
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello back" {
		t.Errorf("text = %q, want hello back", resp.Text)
	}
	if resp.TokensIn != 12 || resp.TokensOut != 34 {
		t.Errorf("usage: in=%d out=%d, want 12/34", resp.TokensIn, resp.TokensOut)
	}
	if resp.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", resp.Provider)
	}
	if resp.CostUSDEstimate <= 0 {
		t.Errorf("cost estimate should be > 0; got %v", resp.CostUSDEstimate)
	}
	if resp.FinishReason != "end_turn" {
		t.Errorf("finish reason = %q, want end_turn", resp.FinishReason)
	}
}

func TestAnthropicHTTPNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	p := NewAnthropicProvider(AnthropicConfig{APIKey: "x", BaseURL: srv.URL})
	_, err := p.Complete(context.Background(), contracts.LLMCompletionRequest{
		Tier:      contracts.LLMTierCheapHot,
		Messages:  []contracts.LLMMessage{{Role: contracts.LLMRoleUser, Content: "x"}},
		MaxTokens: 8,
	})
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status; got %q", err.Error())
	}
}

func TestAnthropicRejectsZeroMaxTokens(t *testing.T) {
	p := NewAnthropicProvider(AnthropicConfig{APIKey: "x", BaseURL: "http://unused"})
	_, err := p.Complete(context.Background(), contracts.LLMCompletionRequest{
		Tier:      contracts.LLMTierCheapHot,
		Messages:  []contracts.LLMMessage{{Role: contracts.LLMRoleUser, Content: "x"}},
		MaxTokens: 0,
	})
	if err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("expected max_tokens error; got %v", err)
	}
}

func TestAnthropicSupportsAllTiers(t *testing.T) {
	p := &AnthropicProvider{}
	for _, tier := range []contracts.LLMTier{contracts.LLMTierCheapHot, contracts.LLMTierSonnet, contracts.LLMTierOpus} {
		if !p.Supports(tier) {
			t.Errorf("anthropic should support %s", tier)
		}
	}
}
