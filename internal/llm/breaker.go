// Package llm provides the multi-provider LLM abstraction described in
// ARCHITECTURE.md §9 Step 3 ("LLM provider abstraction with routing +
// circuit breaker + fallback chain"). Callers — the suggest handler, the
// scoring batch, and webhook classifiers — talk to *Router, which fronts
// per-provider implementations behind a per-provider circuit breaker.
//
// This file contains the breaker. A tiny in-tree implementation was chosen
// over `github.com/sony/gobreaker` to keep the dep graph minimal (the only
// other place a generic breaker would land is the embedding provider, which
// can import the same type) and because the state machine we need is the
// canonical closed → open → half-open → closed loop with no exotic
// half-open concurrency limits. Recorded as a v1 decision in DECISIONS.md.
package llm

import (
	"sync"
	"time"
)

// BreakerState is the public state-machine label surfaced by HealthSnapshot.
type BreakerState int

const (
	// BreakerClosed accepts traffic normally; failures are counted.
	BreakerClosed BreakerState = iota
	// BreakerOpen rejects all traffic until the recovery delay elapses.
	BreakerOpen
	// BreakerHalfOpen lets exactly one probe through; success closes,
	// failure re-opens with a fresh recovery delay.
	BreakerHalfOpen
)

// String renders the state as a lowercase token suitable for JSON.
func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "closed"
	case BreakerOpen:
		return "open"
	case BreakerHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// BreakerConfig is the per-provider tuning. Defaults applied by
// newBreaker if any field is zero, so callers can pass a zero struct and
// get the v1-default (5 failures, 30s recovery) settings.
//
//   - FailureThreshold: number of consecutive failures that trip closed→open.
//   - RecoveryDelay: time spent open before transitioning to half-open.
//   - Now: injectable clock for tests; defaults to time.Now.
type BreakerConfig struct {
	FailureThreshold int
	RecoveryDelay    time.Duration
	Now              func() time.Time
}

const (
	defaultFailureThreshold = 5
	defaultRecoveryDelay    = 30 * time.Second
)

// breaker is the per-provider circuit breaker. Safe for concurrent callers;
// the mutex covers a tiny state transition so contention is not a concern.
type breaker struct {
	mu               sync.Mutex
	state            BreakerState
	consecutiveFails int
	openedAt         time.Time
	cfg              BreakerConfig
}

func newBreaker(cfg BreakerConfig) *breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaultFailureThreshold
	}
	if cfg.RecoveryDelay <= 0 {
		cfg.RecoveryDelay = defaultRecoveryDelay
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &breaker{cfg: cfg}
}

// allow reports whether the next call may proceed. When the breaker is open
// and the recovery delay has elapsed, allow transitions to half-open and
// returns true (the half-open probe).
func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerClosed, BreakerHalfOpen:
		return true
	case BreakerOpen:
		if b.cfg.Now().Sub(b.openedAt) >= b.cfg.RecoveryDelay {
			b.state = BreakerHalfOpen
			return true
		}
		return false
	default:
		return false
	}
}

// success resets the failure counter and closes the breaker.
func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFails = 0
	b.state = BreakerClosed
}

// failure records a failure. In closed state, opens once the threshold is
// reached. In half-open, opens immediately and restarts the recovery delay.
func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFails++
	if b.state == BreakerHalfOpen {
		b.state = BreakerOpen
		b.openedAt = b.cfg.Now()
		return
	}
	if b.consecutiveFails >= b.cfg.FailureThreshold {
		b.state = BreakerOpen
		b.openedAt = b.cfg.Now()
	}
}

// snapshot returns the current state; used by HealthSnapshot.
func (b *breaker) snapshot() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Promote open→half_open lazily so /health reflects "ready to probe."
	if b.state == BreakerOpen && b.cfg.Now().Sub(b.openedAt) >= b.cfg.RecoveryDelay {
		return BreakerHalfOpen
	}
	return b.state
}
