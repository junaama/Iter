package llm

import (
	"testing"
	"time"
)

// fakeClock returns the value mutated by the test.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestBreakerClosedAllowsTraffic(t *testing.T) {
	b := newBreaker(BreakerConfig{})
	if !b.allow() {
		t.Fatal("freshly-constructed breaker should be closed and allow traffic")
	}
}

func TestBreakerOpensAfterNConsecutiveFailures(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	b := newBreaker(BreakerConfig{FailureThreshold: 3, RecoveryDelay: 30 * time.Second, Now: clk.Now})

	for range 2 {
		b.failure()
	}
	if !b.allow() {
		t.Fatalf("after 2 failures (threshold=3) breaker should still be closed")
	}
	if got := b.snapshot(); got != BreakerClosed {
		t.Fatalf("snapshot = %s, want closed", got)
	}

	b.failure()
	if b.allow() {
		t.Fatalf("after 3 failures breaker should be open and reject traffic")
	}
	if got := b.snapshot(); got != BreakerOpen {
		t.Fatalf("snapshot = %s, want open", got)
	}
}

func TestBreakerSuccessResetsFailureCount(t *testing.T) {
	b := newBreaker(BreakerConfig{FailureThreshold: 3})
	b.failure()
	b.failure()
	b.success()
	b.failure()
	b.failure()
	if !b.allow() {
		t.Fatal("two failures with an interleaved success should not open the breaker")
	}
}

func TestBreakerHalfOpensAfterRecoveryDelay(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	b := newBreaker(BreakerConfig{FailureThreshold: 1, RecoveryDelay: 30 * time.Second, Now: clk.Now})

	b.failure()
	if b.allow() {
		t.Fatal("breaker should be open immediately after threshold failure")
	}

	clk.now = clk.now.Add(15 * time.Second)
	if b.allow() {
		t.Fatal("breaker should remain open before recovery delay elapses")
	}

	clk.now = clk.now.Add(20 * time.Second) // total 35s > 30s recovery
	if !b.allow() {
		t.Fatal("breaker should half-open after recovery delay")
	}
	if got := b.snapshot(); got != BreakerHalfOpen {
		t.Fatalf("snapshot = %s, want half_open", got)
	}
}

func TestBreakerHalfOpenSuccessCloses(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	b := newBreaker(BreakerConfig{FailureThreshold: 1, RecoveryDelay: 10 * time.Second, Now: clk.Now})

	b.failure()
	clk.now = clk.now.Add(11 * time.Second)
	if !b.allow() {
		t.Fatal("breaker should half-open")
	}
	b.success()
	if got := b.snapshot(); got != BreakerClosed {
		t.Fatalf("after half-open success, snapshot = %s, want closed", got)
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	b := newBreaker(BreakerConfig{FailureThreshold: 1, RecoveryDelay: 10 * time.Second, Now: clk.Now})

	b.failure()
	clk.now = clk.now.Add(11 * time.Second)
	_ = b.allow() // promote to half-open
	b.failure()   // half-open failure
	if got := b.snapshot(); got != BreakerOpen {
		t.Fatalf("after half-open failure, snapshot = %s, want open", got)
	}
}

func TestBreakerDefaultsApplied(t *testing.T) {
	b := newBreaker(BreakerConfig{})
	if b.cfg.FailureThreshold != defaultFailureThreshold {
		t.Errorf("default FailureThreshold = %d, want %d", b.cfg.FailureThreshold, defaultFailureThreshold)
	}
	if b.cfg.RecoveryDelay != defaultRecoveryDelay {
		t.Errorf("default RecoveryDelay = %s, want %s", b.cfg.RecoveryDelay, defaultRecoveryDelay)
	}
	if b.cfg.Now == nil {
		t.Error("default Now should be non-nil")
	}
}
