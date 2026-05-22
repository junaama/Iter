package embed

import (
	"sync"
	"time"
)

// BreakerState is the public state-machine label surfaced by HealthSnapshot.
// Mirrors internal/llm's breaker but lives here so embed has zero coupling
// to the LLM dependency chain (DECISIONS.md: embedding and LLM are
// different domains; their health must not be shared).
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
//   - FailureThreshold: consecutive failures that trip closed→open.
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

// breaker is the per-provider circuit breaker. Safe for concurrent callers.
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

// snapshot returns the current state; used by HealthSnapshot. Promotes
// open→half_open lazily so /health reflects "ready to probe."
func (b *breaker) snapshot() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == BreakerOpen && b.cfg.Now().Sub(b.openedAt) >= b.cfg.RecoveryDelay {
		return BreakerHalfOpen
	}
	return b.state
}
