package signals_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/iter-dev/iter/internal/signals"
	"github.com/iter-dev/iter/pkg/contracts"
)

// helper: build an event with a deterministic ID and timestamp.
func ev(id, sess string, t contracts.EventType, secondsOffset int) contracts.SessionEvent {
	return contracts.SessionEvent{
		ID:         id,
		SessionID:  sess,
		Type:       t,
		OccurredAt: time.Unix(int64(secondsOffset), 0).UTC(),
	}
}

func subEv(id, sess, parent string, t contracts.EventType, secondsOffset int) contracts.SessionEvent {
	e := ev(id, sess, t, secondsOffset)
	e.ParentSessionID = &parent
	return e
}

func intp(v int) *int             { return &v }
func float64p(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// 1. Basic table-driven aggregation
// ---------------------------------------------------------------------------

func TestAggregate_Table(t *testing.T) {
	cases := []struct {
		name   string
		events []contracts.SessionEvent
		want   contracts.ScoreSignals
	}{
		{
			name:   "empty input → zero-value signals",
			events: nil,
			want:   contracts.ScoreSignals{},
		},
		{
			name: "single peer_reuse",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventPeerReuse, 1),
			},
			want: contracts.ScoreSignals{
				PeerReuseCount: intp(1),
			},
		},
		{
			name: "single self_reuse",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventSelfReuse, 1),
			},
			want: contracts.ScoreSignals{
				SelfReuseCount: intp(1),
			},
		},
		{
			name: "reuse mix counts both",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventPeerReuse, 1),
				ev("e2", "s1", contracts.EventPeerReuse, 2),
				ev("e3", "s1", contracts.EventSelfReuse, 3),
			},
			want: contracts.ScoreSignals{
				PeerReuseCount: intp(2),
				SelfReuseCount: intp(1),
			},
		},
		{
			name: "override_rate = overrides / turns",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventTurnCompleted, 1),
				ev("e2", "s1", contracts.EventTurnCompleted, 2),
				ev("e3", "s1", contracts.EventTurnCompleted, 3),
				ev("e4", "s1", contracts.EventTurnCompleted, 4),
				ev("e5", "s1", contracts.EventUserOverride, 5),
			},
			want: contracts.ScoreSignals{
				OverrideRate: float64p(0.25),
			},
		},
		{
			name: "override_rate clamps to 1.0 when overrides ≥ turns",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventTurnCompleted, 1),
				ev("e2", "s1", contracts.EventUserOverride, 2),
				ev("e3", "s1", contracts.EventUserOverride, 3),
				ev("e4", "s1", contracts.EventUserOverride, 4),
			},
			want: contracts.ScoreSignals{
				OverrideRate: float64p(1.0),
			},
		},
		{
			name: "override without any turn → nil (undefined denominator)",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventUserOverride, 1),
			},
			want: contracts.ScoreSignals{
				// no turns → rate undefined; do not surface
			},
		},
		{
			name: "suggestion_acceptance = accepted / (accepted+rejected)",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventSuggestionAccepted, 1),
				ev("e2", "s1", contracts.EventSuggestionAccepted, 2),
				ev("e3", "s1", contracts.EventSuggestionAccepted, 3),
				ev("e4", "s1", contracts.EventSuggestionRejected, 4),
			},
			want: contracts.ScoreSignals{
				SuggestionAcceptance: float64p(0.75),
			},
		},
		{
			name: "suggestion_acceptance: only rejections → 0.0",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventSuggestionRejected, 1),
				ev("e2", "s1", contracts.EventSuggestionRejected, 2),
			},
			want: contracts.ScoreSignals{
				SuggestionAcceptance: float64p(0.0),
			},
		},
		{
			name: "no suggestion outcome events → nil",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventTurnCompleted, 1),
			},
			want: contracts.ScoreSignals{
				// no acceptance / rejection events → undefined
			},
		},
		{
			name: "irrelevant events (prompt_sent / tool_call / pr_*) do not affect signals",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventPromptSent, 1),
				ev("e2", "s1", contracts.EventToolCall, 2),
				ev("e3", "s1", contracts.EventGitCommit, 3),
				ev("e4", "s1", contracts.EventPRMerged, 4),
				ev("e5", "s1", contracts.EventIncidentLinked, 5),
			},
			want: contracts.ScoreSignals{},
		},
		{
			name: "full mix",
			events: []contracts.SessionEvent{
				ev("e1", "s1", contracts.EventTurnCompleted, 1),
				ev("e2", "s1", contracts.EventTurnCompleted, 2),
				ev("e3", "s1", contracts.EventUserOverride, 3),
				ev("e4", "s1", contracts.EventPeerReuse, 4),
				ev("e5", "s1", contracts.EventPeerReuse, 5),
				ev("e6", "s1", contracts.EventSelfReuse, 6),
				ev("e7", "s1", contracts.EventSuggestionAccepted, 7),
				ev("e8", "s1", contracts.EventSuggestionRejected, 8),
			},
			want: contracts.ScoreSignals{
				PeerReuseCount:       intp(2),
				SelfReuseCount:       intp(1),
				OverrideRate:         float64p(0.5),
				SuggestionAcceptance: float64p(0.5),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := signals.Aggregate(tc.events)
			if !equalSignals(got, tc.want) {
				t.Fatalf("Aggregate mismatch:\n got=%s\nwant=%s", fmtSignals(got), fmtSignals(tc.want))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Order independence (property test, N=1000 shuffles)
// ---------------------------------------------------------------------------

func TestAggregate_OrderIndependence(t *testing.T) {
	base := []contracts.SessionEvent{
		ev("a", "s1", contracts.EventTurnCompleted, 1),
		ev("b", "s1", contracts.EventTurnCompleted, 2),
		ev("c", "s1", contracts.EventTurnCompleted, 3),
		ev("d", "s1", contracts.EventTurnCompleted, 4),
		ev("e", "s1", contracts.EventUserOverride, 5),
		ev("f", "s1", contracts.EventPeerReuse, 6),
		ev("g", "s1", contracts.EventPeerReuse, 7),
		ev("h", "s1", contracts.EventSelfReuse, 8),
		ev("i", "s1", contracts.EventSuggestionAccepted, 9),
		ev("j", "s1", contracts.EventSuggestionRejected, 10),
		ev("k", "s1", contracts.EventPromptSent, 11),
		ev("l", "s1", contracts.EventToolCall, 12),
	}
	want := signals.Aggregate(base)

	// Deterministic shuffle source.
	//nolint:gosec // not cryptographic; we want reproducibility.
	rng := rand.New(rand.NewSource(0xDEADBEEF))

	const N = 1000
	for i := 0; i < N; i++ {
		shuffled := make([]contracts.SessionEvent, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(x, y int) {
			shuffled[x], shuffled[y] = shuffled[y], shuffled[x]
		})

		got := signals.Aggregate(shuffled)
		if !equalSignals(got, want) {
			t.Fatalf("iteration %d: order-dependent output\n got=%s\nwant=%s",
				i, fmtSignals(got), fmtSignals(want))
		}
	}
}

// ---------------------------------------------------------------------------
// 3. Duplicate idempotency: events identified by ID
// ---------------------------------------------------------------------------

func TestAggregate_DuplicateIdempotency(t *testing.T) {
	base := []contracts.SessionEvent{
		ev("a", "s1", contracts.EventTurnCompleted, 1),
		ev("b", "s1", contracts.EventTurnCompleted, 2),
		ev("c", "s1", contracts.EventUserOverride, 3),
		ev("d", "s1", contracts.EventPeerReuse, 4),
		ev("e", "s1", contracts.EventSelfReuse, 5),
		ev("f", "s1", contracts.EventSuggestionAccepted, 6),
		ev("g", "s1", contracts.EventSuggestionRejected, 7),
	}
	want := signals.Aggregate(base)

	doubled := append(append([]contracts.SessionEvent{}, base...), base...)
	got := signals.Aggregate(doubled)
	if !equalSignals(got, want) {
		t.Fatalf("append(events, events...) changed output:\n got=%s\nwant=%s",
			fmtSignals(got), fmtSignals(want))
	}

	// And a 3x replay also stable.
	tripled := append(append(append([]contracts.SessionEvent{}, base...), base...), base...)
	got3 := signals.Aggregate(tripled)
	if !equalSignals(got3, want) {
		t.Fatalf("3x replay changed output:\n got=%s\nwant=%s",
			fmtSignals(got3), fmtSignals(want))
	}
}

// ---------------------------------------------------------------------------
// 4. Empty input
// ---------------------------------------------------------------------------

func TestAggregate_EmptyInput(t *testing.T) {
	// nil slice
	gotNil := signals.Aggregate(nil)
	if !equalSignals(gotNil, contracts.ScoreSignals{}) {
		t.Fatalf("nil input did not return zero-value signals, got=%s", fmtSignals(gotNil))
	}
	// empty slice
	gotEmpty := signals.Aggregate([]contracts.SessionEvent{})
	if !equalSignals(gotEmpty, contracts.ScoreSignals{}) {
		t.Fatalf("empty slice did not return zero-value signals, got=%s", fmtSignals(gotEmpty))
	}
}

// ---------------------------------------------------------------------------
// 5. Single event
// ---------------------------------------------------------------------------

func TestAggregate_SingleEvent(t *testing.T) {
	got := signals.Aggregate([]contracts.SessionEvent{
		ev("e1", "s1", contracts.EventPeerReuse, 1),
	})
	want := contracts.ScoreSignals{PeerReuseCount: intp(1)}
	if !equalSignals(got, want) {
		t.Fatalf("single event:\n got=%s\nwant=%s", fmtSignals(got), fmtSignals(want))
	}
}

// ---------------------------------------------------------------------------
// 6. Subagent independence: subagent events do NOT influence parent aggregation
// ---------------------------------------------------------------------------

func TestAggregate_SubagentIndependence(t *testing.T) {
	parentOnly := []contracts.SessionEvent{
		ev("p1", "parent", contracts.EventTurnCompleted, 1),
		ev("p2", "parent", contracts.EventTurnCompleted, 2),
		ev("p3", "parent", contracts.EventUserOverride, 3),
		ev("p4", "parent", contracts.EventPeerReuse, 4),
	}
	wantParent := signals.Aggregate(parentOnly)

	// Now mix in events that belong to a subagent (different session id,
	// parent_session_id set). Parent aggregation MUST NOT change.
	mixed := append([]contracts.SessionEvent{}, parentOnly...)
	mixed = append(mixed,
		subEv("c1", "child", "parent", contracts.EventTurnCompleted, 10),
		subEv("c2", "child", "parent", contracts.EventUserOverride, 11),
		subEv("c3", "child", "parent", contracts.EventPeerReuse, 12),
		subEv("c4", "child", "parent", contracts.EventSelfReuse, 13),
		subEv("c5", "child", "parent", contracts.EventSuggestionAccepted, 14),
	)

	gotParent := signals.Aggregate(mixed)
	if !equalSignals(gotParent, wantParent) {
		t.Fatalf("subagent events leaked into parent aggregation:\n got=%s\nwant=%s",
			fmtSignals(gotParent), fmtSignals(wantParent))
	}

	// And: AggregateSubagent over the same mixed slice returns subagent-only
	// signals, ignoring the parent events.
	gotChild := signals.AggregateSubagent(mixed)
	wantChild := contracts.ScoreSignals{
		PeerReuseCount:       intp(1),
		SelfReuseCount:       intp(1),
		OverrideRate:         float64p(1.0),
		SuggestionAcceptance: float64p(1.0),
	}
	if !equalSignals(gotChild, wantChild) {
		t.Fatalf("subagent aggregation:\n got=%s\nwant=%s",
			fmtSignals(gotChild), fmtSignals(wantChild))
	}
}

// ---------------------------------------------------------------------------
// 7. Negative-control hygiene: equal-input determinism on a separate seed
// ---------------------------------------------------------------------------

func TestAggregate_DeterminismAcrossCalls(t *testing.T) {
	events := []contracts.SessionEvent{
		ev("a", "s1", contracts.EventPeerReuse, 1),
		ev("b", "s1", contracts.EventTurnCompleted, 2),
		ev("c", "s1", contracts.EventUserOverride, 3),
	}
	first := signals.Aggregate(events)
	for i := 0; i < 50; i++ {
		again := signals.Aggregate(events)
		if !equalSignals(first, again) {
			t.Fatalf("non-deterministic Aggregate on iteration %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// 8. Events with empty ID are accepted (no dedup) — daemon WAL rows may
// arrive before they receive a server-side id; aggregation must still tally
// them. Two events with empty IDs are NOT collapsed.
// ---------------------------------------------------------------------------

func TestAggregate_EmptyIDsNotDeduped(t *testing.T) {
	events := []contracts.SessionEvent{
		ev("", "s1", contracts.EventPeerReuse, 1),
		ev("", "s1", contracts.EventPeerReuse, 2),
		ev("", "s1", contracts.EventPeerReuse, 3),
	}
	got := signals.Aggregate(events)
	want := contracts.ScoreSignals{PeerReuseCount: intp(3)}
	if !equalSignals(got, want) {
		t.Fatalf("empty-ID events must NOT be deduplicated:\n got=%s\nwant=%s",
			fmtSignals(got), fmtSignals(want))
	}
}

// ---------------------------------------------------------------------------
// IsSubagent flag-only path: a SessionEvent with ParentSessionID nil is
// treated as a parent event; with ParentSessionID set to "" is also parent.
// ---------------------------------------------------------------------------

func TestSessionEvent_IsSubagent(t *testing.T) {
	parent := ev("e1", "s1", contracts.EventPeerReuse, 1)
	if parent.IsSubagent() {
		t.Fatalf("event with nil ParentSessionID should not be a subagent")
	}
	empty := ""
	parent.ParentSessionID = &empty
	if parent.IsSubagent() {
		t.Fatalf("event with empty ParentSessionID should not be a subagent")
	}
	p := "parent"
	parent.ParentSessionID = &p
	if !parent.IsSubagent() {
		t.Fatalf("event with non-empty ParentSessionID should be a subagent")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// equalSignals compares two ScoreSignals by value (dereferencing pointer
// fields). It does not compare the Extra map (aggregation never populates it).
func equalSignals(a, b contracts.ScoreSignals) bool {
	return equalFloat(a.Durability7d, b.Durability7d) &&
		equalFloat(a.Durability30d, b.Durability30d) &&
		equalInt(a.PeerReuseCount, b.PeerReuseCount) &&
		equalInt(a.SelfReuseCount, b.SelfReuseCount) &&
		equalFloat(a.OverrideRate, b.OverrideRate) &&
		equalFloat(a.SuggestionAcceptance, b.SuggestionAcceptance) &&
		reflect.DeepEqual(a.Extra, b.Extra)
}

func equalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fmtSignals(s contracts.ScoreSignals) string {
	return fmt.Sprintf(
		"{ durability_7d=%v durability_30d=%v peer_reuse_count=%v self_reuse_count=%v override_rate=%v suggestion_acceptance=%v }",
		fmtFloatP(s.Durability7d),
		fmtFloatP(s.Durability30d),
		fmtIntP(s.PeerReuseCount),
		fmtIntP(s.SelfReuseCount),
		fmtFloatP(s.OverrideRate),
		fmtFloatP(s.SuggestionAcceptance),
	)
}

func fmtFloatP(p *float64) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%v", *p)
}

func fmtIntP(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", *p)
}
