package embed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Router fronts a fixed list of embedding providers behind per-provider
// circuit breakers, with a single ordered fallback chain (no per-tier
// split — embedding has only one quality dimension at v1, unlike LLM
// completions). Construction is via NewRouter; the zero value is unusable.
//
// At v1 the routing strategy is simple: walk the provider chain in
// declared order, ask each one's breaker if traffic is allowed, and
// return the first successful response. A provider error or open breaker
// advances to the next. When every provider has been tried, return
// ErrAllProvidersUnavailable wrapped with the per-provider attempt log
// so the caller can emit "tried [voyage→openai→google], all failed."
type Router struct {
	providers []Provider
	breakers  map[string]*breaker
	priority  []string // provider names in fallback order
	cache     *Cache

	mu sync.RWMutex
}

// RouterConfig wires Provider implementations, a priority chain, an
// optional cache, and the breaker tuning. An empty Priority slice yields
// ErrAllProvidersUnavailable on every call.
type RouterConfig struct {
	Providers  []Provider
	Priority   []string
	Cache      *Cache
	BreakerCfg BreakerConfig
}

// NewRouter builds a Router. Providers not referenced in Priority are
// still registered (so HealthSnapshot can report on them) but will never
// be selected by Embed.
func NewRouter(cfg RouterConfig) *Router {
	r := &Router{
		providers: append([]Provider(nil), cfg.Providers...),
		breakers:  make(map[string]*breaker, len(cfg.Providers)),
		priority:  append([]string(nil), cfg.Priority...),
		cache:     cfg.Cache,
	}
	for _, p := range cfg.Providers {
		r.breakers[p.Name()] = newBreaker(cfg.BreakerCfg)
	}
	return r
}

// providerByName looks up a registered Provider; nil if absent.
func (r *Router) providerByName(name string) Provider {
	for _, p := range r.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Embed walks the provider chain and returns the first successful
// response, with SHA256-keyed Redis caching for the common batch-of-one
// shape (the dominant call from the embedding worker per
// ARCHITECTURE.md §4 — items pulled 1-by-1 from `embed:queue`).
//
// Cache strategy:
//   - For len(req.Inputs) == 1: check cache before any provider call;
//     write to cache on success (fire-and-forget).
//   - For len(req.Inputs) > 1: bypass cache entirely. Multi-input batches
//     are a future optimization; mixing partial cache hits with provider
//     calls multiplies code complexity without a v1 caller.
//
// Each attempt is bounded by ctx.Deadline; the router never imposes its
// own timeout because the caller owns the latency budget (suggest path:
// ≤1s P99 total).
//
// Error contract: every non-success return wraps ErrAllProvidersUnavailable
// so callers can pattern-match it with errors.Is.
func (r *Router) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if len(req.Inputs) == 0 {
		return EmbedResponse{}, fmt.Errorf("embed: inputs must be non-empty")
	}

	r.mu.RLock()
	chain := append([]string(nil), r.priority...)
	r.mu.RUnlock()

	if len(chain) == 0 {
		return EmbedResponse{}, fmt.Errorf("router: %w", ErrAllProvidersUnavailable)
	}

	// Honor an already-expired context before doing any work.
	if err := ctx.Err(); err != nil {
		return EmbedResponse{}, fmt.Errorf("router: ctx: %w: %w", err, ErrAllProvidersUnavailable)
	}

	// Single-input fast path: cache lookup keyed by (model, input).
	// We use the requested model verbatim — empty string is a valid
	// cache key meaning "provider default," which is stable across calls.
	singleInput := len(req.Inputs) == 1
	if singleInput && r.cache != nil {
		if vec, ok := r.cache.Get(ctx, req.Model, req.Inputs[0]); ok {
			return EmbedResponse{Vectors: [][]float32{vec}}, nil
		}
	}

	attempts := make([]string, 0, len(chain))
	for _, name := range chain {
		p := r.providerByName(name)
		if p == nil {
			attempts = append(attempts, name+":unregistered")
			continue
		}
		b := r.breakers[name]
		if !b.allow() {
			attempts = append(attempts, name+":breaker_open")
			continue
		}
		// Re-check ctx between attempts so a slow predecessor doesn't
		// cause us to over-budget the user.
		if err := ctx.Err(); err != nil {
			attempts = append(attempts, name+":ctx_"+err.Error())
			break
		}
		resp, err := p.Embed(ctx, req)
		if err != nil {
			b.failure()
			if errors.Is(err, ErrRateLimited) {
				return EmbedResponse{}, fmt.Errorf("%s: %w", name, ErrRateLimited)
			}
			attempts = append(attempts, fmt.Sprintf("%s:%s", name, errorTag(err)))
			continue
		}
		b.success()
		if singleInput && r.cache != nil && len(resp.Vectors) == 1 {
			r.cache.Set(ctx, req.Model, req.Inputs[0], resp.Vectors[0])
		}
		return resp, nil
	}

	return EmbedResponse{}, fmt.Errorf("tried [%s]: %w", strings.Join(attempts, ","), ErrAllProvidersUnavailable)
}

// errorTag produces a short, log-friendly tag for the attempts string.
func errorTag(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrProviderNotConfigured):
		return "unconfigured"
	case errors.Is(err, ErrProviderNotImplemented):
		return "not_implemented"
	default:
		return "error"
	}
}
