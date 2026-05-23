package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/langfuse"
	"github.com/iter-dev/iter/pkg/contracts"
)

// Tracer is the narrow boundary the router uses to emit observability
// events. Satisfied by *langfuse.Client today; kept as an interface so
// tests can plug in an in-memory recorder and so a future swap (OTel,
// no-op stub) doesn't ripple through router code. A nil Tracer is the
// "tracing off" signal — the router skips all emission overhead.
type Tracer interface {
	Submit(e langfuse.Event)
}

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

	// Tracer, when non-nil, receives one generation event per provider
	// attempt that actually invoked the provider (i.e. skipped attempts
	// like "breaker_open" or "tier_unsupported" do NOT emit). Nil is the
	// "tracing disabled" state and the LLM path is unchanged.
	Tracer Tracer

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

	// Tracer is optional. When non-nil the router emits one Langfuse
	// generation per provider attempt that actually invoked the
	// provider. Nil = tracing off (LLM path is unchanged).
	Tracer Tracer
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
		Tracer:    cfg.Tracer,
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
		// Per-attempt trace ID — one Langfuse generation per provider
		// invocation. A retry across providers shares no trace because
		// upstream context plumbing isn't here yet (v1).
		startedAt := time.Now()
		resp, err := p.Complete(ctx, req)
		if err != nil {
			b.failure()
			attempts = append(attempts, fmt.Sprintf("%s:%s", name, errorTag(err)))
			r.emit(ctx, name, req, contracts.LLMCompletionResponse{}, startedAt, time.Now(), err)
			continue
		}
		b.success()
		if resp.Provider == "" {
			resp.Provider = name
		}
		r.emit(ctx, name, req, resp, startedAt, time.Now(), nil)
		return resp, nil
	}

	return contracts.LLMCompletionResponse{}, fmt.Errorf("tier %q tried [%s]: %w", req.Tier, strings.Join(attempts, ","), ErrAllProvidersUnavailable)
}

// emit submits one Langfuse generation event for a provider attempt.
// Skips silently when no Tracer is configured (nil-receiver-safe on the
// tracer side, but we still short-circuit to avoid the json.Marshal of
// the messages). All errors stay inside this function: emission can
// never fail an LLM call.
func (r *Router) emit(ctx context.Context, provider string, req contracts.LLMCompletionRequest, resp contracts.LLMCompletionResponse, startedAt, endedAt time.Time, callErr error) {
	if r.Tracer == nil {
		return
	}
	defer func() {
		// Defensive: if anything in event construction panics, never
		// propagate to the caller. The LLM path must remain isolated
		// from observability bugs.
		_ = recover()
	}()

	// Encode messages as JSON so the Langfuse UI shows the full chat
	// rather than the last user turn. Fall back to the last message's
	// content if marshaling fails (shouldn't, but defensively).
	input := ""
	if raw, mErr := json.Marshal(req.Messages); mErr == nil {
		input = string(raw)
	} else if len(req.Messages) > 0 {
		input = req.Messages[len(req.Messages)-1].Content
	}

	gen := langfuse.Generation{
		TraceID:   uuid.NewString(),
		Name:      provider + "." + string(req.Tier),
		StartTime: startedAt,
		EndTime:   endedAt,
		Model:     resp.Model,
		Input:     input,
		Output:    resp.Text,
		Usage: langfuse.Usage{
			Input:  resp.TokensIn,
			Output: resp.TokensOut,
			Total:  resp.TokensIn + resp.TokensOut,
		},
		Metadata: map[string]string{
			"tier":     string(req.Tier),
			"provider": provider,
		},
	}
	if callErr != nil {
		gen.Level = langfuse.LevelError
		gen.StatusMessage = callErr.Error()
	}
	// Tag the tenant when the request context carries an authenticated
	// principal — handy for filtering in the Langfuse UI.
	if p, ok := contracts.PrincipalFromContext(ctx); ok && p.TenantID != (uuid.UUID{}) {
		gen.Metadata["tenant_id"] = p.TenantID.String()
	}

	r.Tracer.Submit(langfuse.NewGenerationEvent(gen))
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
