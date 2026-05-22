package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/iter-dev/iter/pkg/contracts"
)

func cheapReq() contracts.LLMCompletionRequest {
	return contracts.LLMCompletionRequest{
		Tier:      contracts.LLMTierCheapHot,
		Messages:  []contracts.LLMMessage{{Role: contracts.LLMRoleUser, Content: "hi"}},
		MaxTokens: 16,
	}
}

func TestRouterReturnsFirstSuccess(t *testing.T) {
	first := &StubProvider{NameValue: "a", Default: contracts.LLMCompletionResponse{Text: "from-a"}}
	second := &StubProvider{NameValue: "b", Default: contracts.LLMCompletionResponse{Text: "from-b"}}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierCheapHot: {"a", "b"}},
	})

	resp, err := r.Complete(context.Background(), cheapReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "from-a" {
		t.Errorf("got text %q, want from-a", resp.Text)
	}
	if resp.Provider != "a" {
		t.Errorf("got provider %q, want a", resp.Provider)
	}
	if second.Calls() != 0 {
		t.Errorf("second provider was called %d times; want 0", second.Calls())
	}
}

func TestRouterFallsThroughOnError(t *testing.T) {
	first := &StubProvider{NameValue: "a", FailWith: errors.New("boom")}
	second := &StubProvider{NameValue: "b", Default: contracts.LLMCompletionResponse{Text: "from-b"}}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierCheapHot: {"a", "b"}},
	})

	resp, err := r.Complete(context.Background(), cheapReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "b" {
		t.Errorf("got provider %q, want b", resp.Provider)
	}
	if first.Calls() != 1 || second.Calls() != 1 {
		t.Errorf("calls: a=%d b=%d, want 1/1", first.Calls(), second.Calls())
	}
}

func TestRouterSkipsTierUnsupportedProviders(t *testing.T) {
	cheapOnly := &StubProvider{NameValue: "cheap", Tiers: []contracts.LLMTier{contracts.LLMTierCheapHot}}
	sonnetOnly := &StubProvider{
		NameValue: "sonnet",
		Tiers:     []contracts.LLMTier{contracts.LLMTierSonnet},
		Default:   contracts.LLMCompletionResponse{Text: "sonnet-ok"},
	}
	r := NewRouter(RouterConfig{
		Providers: []Provider{cheapOnly, sonnetOnly},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierSonnet: {"cheap", "sonnet"}},
	})

	resp, err := r.Complete(context.Background(), contracts.LLMCompletionRequest{
		Tier:      contracts.LLMTierSonnet,
		Messages:  []contracts.LLMMessage{{Role: contracts.LLMRoleUser, Content: "x"}},
		MaxTokens: 8,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "sonnet" {
		t.Errorf("got provider %q, want sonnet", resp.Provider)
	}
	if cheapOnly.Calls() != 0 {
		t.Errorf("cheap-only provider was called %d times; want 0 (tier mismatch)", cheapOnly.Calls())
	}
}

func TestRouterAllUnavailableReturnsSentinel(t *testing.T) {
	first := &StubProvider{NameValue: "a", FailWith: errors.New("boom")}
	second := &StubProvider{NameValue: "b", FailWith: errors.New("boom")}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierCheapHot: {"a", "b"}},
	})

	_, err := r.Complete(context.Background(), cheapReq())
	if err == nil {
		t.Fatal("expected error when every provider fails")
	}
	if !errors.Is(err, ErrAllProvidersUnavailable) {
		t.Fatalf("error = %v, want errors.Is(_, ErrAllProvidersUnavailable)", err)
	}
	if !strings.Contains(err.Error(), "a:") || !strings.Contains(err.Error(), "b:") {
		t.Errorf("error message should enumerate attempted providers; got %q", err.Error())
	}
}

func TestRouterEmptyChainReturnsSentinel(t *testing.T) {
	r := NewRouter(RouterConfig{
		Providers: []Provider{&StubProvider{NameValue: "a"}},
		Priority:  map[contracts.LLMTier][]string{}, // nothing for CheapHot
	})
	_, err := r.Complete(context.Background(), cheapReq())
	if !errors.Is(err, ErrAllProvidersUnavailable) {
		t.Fatalf("empty chain should yield ErrAllProvidersUnavailable; got %v", err)
	}
}

func TestRouterAlreadyCanceledContext(t *testing.T) {
	first := &StubProvider{NameValue: "a", Default: contracts.LLMCompletionResponse{Text: "x"}}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierCheapHot: {"a"}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Complete(ctx, cheapReq())
	if !errors.Is(err, ErrAllProvidersUnavailable) {
		t.Fatalf("expected wrapped ErrAllProvidersUnavailable on canceled ctx; got %v", err)
	}
	if first.Calls() != 0 {
		t.Errorf("provider should not be called when ctx already canceled; got %d calls", first.Calls())
	}
}

func TestRouterBreakerOpensAndSkipsProvider(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	first := &StubProvider{NameValue: "a", FailWith: errors.New("boom")}
	second := &StubProvider{NameValue: "b", Default: contracts.LLMCompletionResponse{Text: "from-b"}}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierCheapHot: {"a", "b"}},
		BreakerCfg: BreakerConfig{
			FailureThreshold: 2,
			RecoveryDelay:    60 * time.Second,
			Now:              clk.Now,
		},
	})

	// Two calls trip the breaker on "a"; both fall through and "b" answers.
	for range 2 {
		if _, err := r.Complete(context.Background(), cheapReq()); err != nil {
			t.Fatalf("unexpected error during warmup: %v", err)
		}
	}
	if first.Calls() != 2 {
		t.Fatalf("first provider should have been called 2x before breaker opens; got %d", first.Calls())
	}

	// Third call: breaker on "a" is open; router should skip it without calling.
	if _, err := r.Complete(context.Background(), cheapReq()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.Calls() != 2 {
		t.Errorf("first provider should NOT have been called once breaker opened; got %d", first.Calls())
	}
	if second.Calls() != 3 {
		t.Errorf("second provider should have absorbed all 3 calls; got %d", second.Calls())
	}
}

func TestRouterUnregisteredProviderNameInChain(t *testing.T) {
	real := &StubProvider{NameValue: "a", Default: contracts.LLMCompletionResponse{Text: "ok"}}
	r := NewRouter(RouterConfig{
		Providers: []Provider{real},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierCheapHot: {"ghost", "a"}},
	})
	resp, err := r.Complete(context.Background(), cheapReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "a" {
		t.Errorf("expected fall-through past unregistered name; got %q", resp.Provider)
	}
}
