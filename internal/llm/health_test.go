package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iter-dev/iter/pkg/contracts"
)

func TestHealthSnapshotReflectsBreakerState(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	bad := &StubProvider{NameValue: "bad", FailWith: errors.New("boom")}
	good := &StubProvider{NameValue: "good", Default: contracts.LLMCompletionResponse{Text: "ok"}}
	r := NewRouter(RouterConfig{
		Providers: []Provider{bad, good},
		Priority:  map[contracts.LLMTier][]string{contracts.LLMTierCheapHot: {"bad", "good"}},
		BreakerCfg: BreakerConfig{
			FailureThreshold: 1,
			RecoveryDelay:    30 * time.Second,
			Now:              clk.Now,
		},
	})

	snap := r.HealthSnapshot()
	if snap["bad"] != StatusOK || snap["good"] != StatusOK {
		t.Errorf("initial snapshot should be all ok; got %+v", snap)
	}

	// Drive one call to trip bad's breaker.
	if _, err := r.Complete(context.Background(), cheapReq()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	snap = r.HealthSnapshot()
	if snap["bad"] != StatusDown {
		t.Errorf("bad provider should be down after breaker opens; got %v", snap["bad"])
	}
	if snap["good"] != StatusOK {
		t.Errorf("good provider should still be ok; got %v", snap["good"])
	}

	// Advance past recovery delay; snapshot should now read degraded
	// (ready to half-open) WITHOUT us needing to make a request.
	clk.now = clk.now.Add(31 * time.Second)
	snap = r.HealthSnapshot()
	if snap["bad"] != StatusDegraded {
		t.Errorf("after recovery delay bad provider should be degraded; got %v", snap["bad"])
	}
}

func TestHealthSnapshotIncludesUnconfiguredProviders(t *testing.T) {
	// A provider registered but never invoked still appears in the snapshot
	// as "ok" — it has had zero failures.
	p := &StubProvider{NameValue: "idle"}
	r := NewRouter(RouterConfig{Providers: []Provider{p}})
	snap := r.HealthSnapshot()
	if got, ok := snap["idle"]; !ok || got != StatusOK {
		t.Errorf("snapshot[idle] = %v, present=%v; want ok/true", got, ok)
	}
}
