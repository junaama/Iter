package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Router fronts a fixed list of providers behind per-provider circuit
// breakers and a per-tier priority order. Construction is via NewRouter;
// the zero value is unusable.
//
// At v1 the routing strategy is simple: walk the per-tier provider chain
// in declared order, ask each one's breaker if traffic is allowed, and
// return the first successful response. A provider error or open breaker
// advances to the next. When every provider in the chain has been tried,
// return ErrAllProvidersUnavailable wrapped with the per-provider attempt
// log so the caller can emit "tried [anthropic→google→openai], all failed."
type Router struct {
	providers []Provider
	breakers  map[string]*breaker
	priority  map[contracts.LLMTier][]string // provider names in order per tier
	cfg       BreakerConfig

	mu sync.RWMutex // guards priority (mutated only by NewRouter today)
}

// RouterConfig wires Provider implementations and per-tier priority. An
// empty priority slice for a tier yields ErrAllProvidersUnavailable on
// every call for that tier — which is the right behavior: deliberately
// nothing configured = deliberately no answer.
//
// BreakerCfg is applied uniformly to every provider; per-provider tuning
// is not a v1 requirement (DECISIONS.md).
type RouterConfig struct {
	Providers  []Provider
	Priority   map[contracts.LLMTier][]string
	BreakerCfg BreakerConfig
}

// NewRouter builds a Router. Providers not referenced in Priority are
// still registered (so HealthSnapshot can report on them) but will never
// be selected by Complete.
func NewRouter(cfg RouterConfig) *Router {
	r := &Router{
		providers: append([]Provider(nil), cfg.Providers...),
		breakers:  make(map[string]*breaker, len(cfg.Providers)),
		priority:  make(map[contracts.LLMTier][]string, len(cfg.Priority)),
		cfg:       cfg.BreakerCfg,
	}
	for _, p := range cfg.Providers {
		r.breakers[p.Name()] = newBreaker(cfg.BreakerCfg)
	}
	for tier, names := range cfg.Priority {
		r.priority[tier] = append([]string(nil), names...)
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

// Complete walks the per-tier provider chain and returns the first
// successful response. Each attempt is bounded by ctx.Deadline; the
// router never imposes its own timeout because the caller (the suggest
// handler) owns the user-facing latency budget.
//
// Error contract: every non-success return wraps ErrAllProvidersUnavailable
// so callers can pattern-match it with errors.Is. The error message
// enumerates attempted providers and their per-attempt failure reason.
func (r *Router) Complete(ctx context.Context, req contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error) {
	r.mu.RLock()
	chain := r.priority[req.Tier]
	r.mu.RUnlock()

	if len(chain) == 0 {
		return contracts.LLMCompletionResponse{}, fmt.Errorf("tier %q: %w", req.Tier, ErrAllProvidersUnavailable)
	}

	// Honor an already-expired context before burning attempts.
	if err := ctx.Err(); err != nil {
		return contracts.LLMCompletionResponse{}, fmt.Errorf("tier %q: ctx: %w: %w", req.Tier, err, ErrAllProvidersUnavailable)
	}

	attempts := make([]string, 0, len(chain))
	for _, name := range chain {
		p := r.providerByName(name)
		if p == nil {
			attempts = append(attempts, name+":unregistered")
			continue
		}
		if !p.Supports(req.Tier) {
			attempts = append(attempts, name+":tier_unsupported")
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
		resp, err := p.Complete(ctx, req)
		if err != nil {
			b.failure()
			attempts = append(attempts, fmt.Sprintf("%s:%s", name, errorTag(err)))
			continue
		}
		b.success()
		if resp.Provider == "" {
			resp.Provider = name
		}
		return resp, nil
	}

	return contracts.LLMCompletionResponse{}, fmt.Errorf("tier %q tried [%s]: %w", req.Tier, strings.Join(attempts, ","), ErrAllProvidersUnavailable)
}

// errorTag produces a short, log-friendly tag for the attempts string.
// Full error details are still wrapped via fmt.Errorf if a caller wants
// errors.Is — this is just for the human-readable enumeration.
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
