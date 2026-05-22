package embed

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func simpleReq() EmbedRequest {
	return EmbedRequest{Model: "voyage-code-3", Inputs: []string{"hello"}}
}

func TestRouterReturnsFirstSuccess(t *testing.T) {
	first := &stubProvider{name: "a"}
	second := &stubProvider{name: "b"}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  []string{"a", "b"},
	})

	resp, err := r.Embed(context.Background(), simpleReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Vectors) != 1 {
		t.Fatalf("got %d vectors, want 1", len(resp.Vectors))
	}
	if second.Calls() != 0 {
		t.Errorf("second provider was called %d times; want 0", second.Calls())
	}
}

func TestRouterFallsThroughOnError(t *testing.T) {
	first := &stubProvider{name: "a", failWith: errors.New("boom")}
	second := &stubProvider{name: "b"}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  []string{"a", "b"},
	})

	_, err := r.Embed(context.Background(), simpleReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.Calls() != 1 || second.Calls() != 1 {
		t.Errorf("calls: a=%d b=%d, want 1/1", first.Calls(), second.Calls())
	}
}

func TestRouterReturnsRateLimitWithoutFallback(t *testing.T) {
	first := &stubProvider{name: "a", failWith: ErrRateLimited}
	second := &stubProvider{name: "b"}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  []string{"a", "b"},
	})

	_, err := r.Embed(context.Background(), simpleReq())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("error = %v, want ErrRateLimited", err)
	}
	if second.Calls() != 0 {
		t.Errorf("second provider was called after rate limit; calls=%d want 0", second.Calls())
	}
}

func TestRouterAllUnavailableReturnsSentinel(t *testing.T) {
	first := &stubProvider{name: "a", failWith: errors.New("boom")}
	second := &stubProvider{name: "b", failWith: errors.New("boom")}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  []string{"a", "b"},
	})

	_, err := r.Embed(context.Background(), simpleReq())
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
	r := NewRouter(RouterConfig{Providers: []Provider{&stubProvider{name: "a"}}})
	_, err := r.Embed(context.Background(), simpleReq())
	if !errors.Is(err, ErrAllProvidersUnavailable) {
		t.Fatalf("empty chain should yield ErrAllProvidersUnavailable; got %v", err)
	}
}

func TestRouterEmptyInputs(t *testing.T) {
	r := NewRouter(RouterConfig{
		Providers: []Provider{&stubProvider{name: "a"}},
		Priority:  []string{"a"},
	})
	_, err := r.Embed(context.Background(), EmbedRequest{})
	if err == nil {
		t.Fatal("empty inputs should error")
	}
}

func TestRouterAlreadyCanceledContext(t *testing.T) {
	first := &stubProvider{name: "a"}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first},
		Priority:  []string{"a"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Embed(ctx, simpleReq())
	if !errors.Is(err, ErrAllProvidersUnavailable) {
		t.Fatalf("expected wrapped ErrAllProvidersUnavailable on canceled ctx; got %v", err)
	}
	if first.Calls() != 0 {
		t.Errorf("provider should not be called when ctx already canceled; got %d calls", first.Calls())
	}
}

func TestRouterBreakerOpensAndSkipsProvider(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	first := &stubProvider{name: "a", failWith: errors.New("boom")}
	second := &stubProvider{name: "b"}
	r := NewRouter(RouterConfig{
		Providers: []Provider{first, second},
		Priority:  []string{"a", "b"},
		BreakerCfg: BreakerConfig{
			FailureThreshold: 2,
			RecoveryDelay:    60 * time.Second,
			Now:              clk.Now,
		},
	})

	// Two calls trip the breaker on "a"; both fall through and "b" answers.
	for range 2 {
		if _, err := r.Embed(context.Background(), simpleReq()); err != nil {
			t.Fatalf("unexpected error during warmup: %v", err)
		}
	}
	if first.Calls() != 2 {
		t.Fatalf("first provider should have been called 2x before breaker opens; got %d", first.Calls())
	}

	// Third call: breaker on "a" is open; router should skip it without calling.
	if _, err := r.Embed(context.Background(), simpleReq()); err != nil {
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
	real := &stubProvider{name: "a"}
	r := NewRouter(RouterConfig{
		Providers: []Provider{real},
		Priority:  []string{"ghost", "a"},
	})
	if _, err := r.Embed(context.Background(), simpleReq()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if real.Calls() != 1 {
		t.Errorf("real provider should have been called; got %d", real.Calls())
	}
}

func TestRouterCacheHitAvoidsProviderCall(t *testing.T) {
	prov := &stubProvider{name: "a"}
	rdb := newFakeRedis()
	r := NewRouter(RouterConfig{
		Providers: []Provider{prov},
		Priority:  []string{"a"},
		Cache:     NewCache(CacheConfig{Redis: rdb}),
	})

	// First call: cache miss, provider invoked once, cache populated.
	resp, err := r.Embed(context.Background(), simpleReq())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if prov.Calls() != 1 {
		t.Fatalf("first call should hit provider once; got %d", prov.Calls())
	}
	if !waitForSetCalls(rdb, 1, 500*time.Millisecond) {
		t.Fatalf("cache Set was not invoked after first call")
	}

	// Second call: cache hit, provider NOT invoked.
	resp2, err := r.Embed(context.Background(), simpleReq())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if prov.Calls() != 1 {
		t.Errorf("second call should hit cache, provider Calls=%d (want 1)", prov.Calls())
	}
	if len(resp2.Vectors) != 1 || len(resp2.Vectors[0]) != len(resp.Vectors[0]) {
		t.Errorf("cache hit returned wrong vector shape")
	}
	for i := range resp.Vectors[0] {
		if resp.Vectors[0][i] != resp2.Vectors[0][i] {
			t.Errorf("vector mismatch at %d: %v != %v", i, resp.Vectors[0][i], resp2.Vectors[0][i])
		}
	}
}

func TestRouterCacheWriteFailureDoesNotBlock(t *testing.T) {
	prov := &stubProvider{name: "a"}
	rdb := newFakeRedis()
	rdb.setErr = errors.New("redis broken")
	r := NewRouter(RouterConfig{
		Providers: []Provider{prov},
		Priority:  []string{"a"},
		Cache:     NewCache(CacheConfig{Redis: rdb}),
	})

	resp, err := r.Embed(context.Background(), simpleReq())
	if err != nil {
		t.Fatalf("Embed should succeed even when cache write fails: %v", err)
	}
	if len(resp.Vectors) != 1 {
		t.Errorf("expected 1 vector, got %d", len(resp.Vectors))
	}
	// Wait briefly for the fire-and-forget goroutine to attempt its Set.
	_ = waitForSetCalls(rdb, 1, 200*time.Millisecond)
}

func TestRouterMultiInputBypassesCache(t *testing.T) {
	prov := &stubProvider{name: "a"}
	rdb := newFakeRedis()
	r := NewRouter(RouterConfig{
		Providers: []Provider{prov},
		Priority:  []string{"a"},
		Cache:     NewCache(CacheConfig{Redis: rdb}),
	})

	req := EmbedRequest{Model: "voyage-code-3", Inputs: []string{"a", "b"}}
	if _, err := r.Embed(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := r.Embed(context.Background(), req); err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	// Both calls should hit the provider — multi-input bypasses cache.
	if prov.Calls() != 2 {
		t.Errorf("multi-input call should bypass cache; provider Calls=%d (want 2)", prov.Calls())
	}
	if rdb.setCallCount() != 0 {
		t.Errorf("multi-input call should not write to cache; setCalls=%d (want 0)", rdb.setCallCount())
	}
}
