//go:build ignore
// +build ignore

// Generate the golden-signals fixture for modal/scoring_test.py.
//
// Why: the Modal Python port of internal/signals + internal/scoring must
// produce *byte-identical* signal aggregates and within-tolerance composite
// scores for hand-curated event fixtures. The Go implementation is the
// canonical reference (issues 008 + 011), so we drive the fixture generation
// from Go and commit the JSON output for Python to compare against.
//
// Run:
//
//	go run scripts/gen-golden-signals.go > modal/testdata/golden_signals.json
//
// The script is gated by the `ignore` build tag so `go build ./...` /
// `go test ./...` ignore it. Re-run any time the signals or scoring formulas
// change in Go — a stale golden file fails the Python tests loudly.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/iter-dev/iter/internal/scoring"
	"github.com/iter-dev/iter/internal/signals"
	"github.com/iter-dev/iter/pkg/contracts"
)

type fixtureEvent struct {
	ID         string `json:"id"`
	EventType  string `json:"event_type"`
	IsSubagent bool   `json:"is_subagent,omitempty"`
}

type fixtureSignals struct {
	Durability7d         *float64 `json:"durability_7d"`
	Durability30d        *float64 `json:"durability_30d"`
	PeerReuseCount       *int     `json:"peer_reuse_count"`
	SelfReuseCount       *int     `json:"self_reuse_count"`
	OverrideRate         *float64 `json:"override_rate"`
	SuggestionAcceptance *float64 `json:"suggestion_acceptance"`
}

type goldenCase struct {
	Name      string         `json:"name"`
	Events    []fixtureEvent `json:"events"`
	Signals   fixtureSignals `json:"signals"`
	Composite float64        `json:"composite"`
}

func toFixtureSignals(s contracts.ScoreSignals) fixtureSignals {
	return fixtureSignals{
		Durability7d:         s.Durability7d,
		Durability30d:        s.Durability30d,
		PeerReuseCount:       s.PeerReuseCount,
		SelfReuseCount:       s.SelfReuseCount,
		OverrideRate:         s.OverrideRate,
		SuggestionAcceptance: s.SuggestionAcceptance,
	}
}

func goEvents(events []fixtureEvent) []contracts.SessionEvent {
	out := make([]contracts.SessionEvent, 0, len(events))
	parent := "parent"
	for i, e := range events {
		se := contracts.SessionEvent{
			ID:         e.ID,
			SessionID:  "s1",
			Type:       contracts.EventType(e.EventType),
			OccurredAt: time.Unix(int64(i), 0).UTC(),
		}
		if e.IsSubagent {
			se.ParentSessionID = &parent
		}
		out = append(out, se)
	}
	return out
}

func main() {
	cases := []struct {
		name   string
		events []fixtureEvent
	}{
		{
			name:   "empty",
			events: nil,
		},
		{
			name: "single_peer_reuse",
			events: []fixtureEvent{
				{ID: "e1", EventType: "peer_reuse"},
			},
		},
		{
			name: "single_self_reuse",
			events: []fixtureEvent{
				{ID: "e1", EventType: "self_reuse"},
			},
		},
		{
			name: "override_rate_25pct",
			events: []fixtureEvent{
				{ID: "e1", EventType: "turn_completed"},
				{ID: "e2", EventType: "turn_completed"},
				{ID: "e3", EventType: "turn_completed"},
				{ID: "e4", EventType: "turn_completed"},
				{ID: "e5", EventType: "user_override"},
			},
		},
		{
			name: "override_rate_clamps_to_one",
			events: []fixtureEvent{
				{ID: "e1", EventType: "turn_completed"},
				{ID: "e2", EventType: "user_override"},
				{ID: "e3", EventType: "user_override"},
				{ID: "e4", EventType: "user_override"},
			},
		},
		{
			name: "suggestion_acceptance_75pct",
			events: []fixtureEvent{
				{ID: "e1", EventType: "suggestion_accepted"},
				{ID: "e2", EventType: "suggestion_accepted"},
				{ID: "e3", EventType: "suggestion_accepted"},
				{ID: "e4", EventType: "suggestion_rejected"},
			},
		},
		{
			name: "full_mix",
			events: []fixtureEvent{
				{ID: "e1", EventType: "turn_completed"},
				{ID: "e2", EventType: "turn_completed"},
				{ID: "e3", EventType: "user_override"},
				{ID: "e4", EventType: "peer_reuse"},
				{ID: "e5", EventType: "peer_reuse"},
				{ID: "e6", EventType: "self_reuse"},
				{ID: "e7", EventType: "suggestion_accepted"},
				{ID: "e8", EventType: "suggestion_rejected"},
			},
		},
		{
			name: "duplicate_ids_dedup",
			events: []fixtureEvent{
				{ID: "e1", EventType: "peer_reuse"},
				{ID: "e1", EventType: "peer_reuse"},
				{ID: "e1", EventType: "peer_reuse"},
				{ID: "e2", EventType: "self_reuse"},
			},
		},
		{
			name: "empty_ids_not_deduped",
			events: []fixtureEvent{
				{ID: "", EventType: "peer_reuse"},
				{ID: "", EventType: "peer_reuse"},
				{ID: "", EventType: "peer_reuse"},
			},
		},
		{
			name: "subagent_events_ignored_for_parent",
			events: []fixtureEvent{
				{ID: "p1", EventType: "peer_reuse"},
				{ID: "c1", EventType: "peer_reuse", IsSubagent: true},
				{ID: "c2", EventType: "self_reuse", IsSubagent: true},
			},
		},
		{
			name: "irrelevant_events_zero_signals",
			events: []fixtureEvent{
				{ID: "e1", EventType: "prompt_sent"},
				{ID: "e2", EventType: "tool_call"},
				{ID: "e3", EventType: "git_commit"},
			},
		},
	}

	golden := make([]goldenCase, 0, len(cases))
	for _, c := range cases {
		sigs := signals.Aggregate(goEvents(c.events))
		comp := scoring.Composite(contracts.CompositeScoreInputs{
			Durability7d:         sigs.Durability7d,
			Durability30d:        sigs.Durability30d,
			PeerReuseCount:       derefIntOr(sigs.PeerReuseCount, 0),
			SelfReuseCount:       derefIntOr(sigs.SelfReuseCount, 0),
			OverrideRate:         sigs.OverrideRate,
			SuggestionAcceptance: sigs.SuggestionAcceptance,
		})

		golden = append(golden, goldenCase{
			Name:      c.name,
			Events:    c.events,
			Signals:   toFixtureSignals(sigs),
			Composite: comp.CompositeScore,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(golden); err != nil {
		fmt.Fprintln(os.Stderr, "encode failed:", err)
		os.Exit(1)
	}
}

func derefIntOr(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}
