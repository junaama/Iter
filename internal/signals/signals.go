// Package signals folds an unordered list of session events into a single
// ScoreSignals value consumed by the scoring function in internal/scoring.
//
// Contract:
//
//   - Pure: no I/O, no clocks, no globals. Same input → same output.
//   - Order-independent: shuffling the input list yields the same output.
//     Events arrive out of order over WebSocket; aggregation must not care.
//   - Idempotent on duplicates: events are deduplicated by SessionEvent.ID
//     before any counter is incremented. Appending a duplicate slice produces
//     the same output (ARCHITECTURE.md §9 Step 5 "replay (upsert)").
//   - Subagent-aware: SessionEvent.IsSubagent() partitions the input. Use
//     Aggregate for the parent session's signals; use AggregateSubagent for
//     subagent-only signals. The two outputs are independent
//     (ARCHITECTURE.md §9 Step 5 "subagent independent scoring").
//
// Signals derived from a single session's events:
//
//   - peer_reuse_count       : count of EventPeerReuse           (int counter)
//   - self_reuse_count       : count of EventSelfReuse           (int counter)
//   - override_rate          : EventUserOverride / EventTurnCompleted, clamped to [0,1]
//   - suggestion_acceptance  : accepted / (accepted + rejected)
//
// Signals NOT derived here (left nil): durability_7d, durability_30d.
// Durability is computed over windows across many sessions and is the
// responsibility of the nightly batch, not single-session aggregation.
package signals

import "github.com/iter-dev/iter/pkg/contracts"

// Aggregate folds the supplied session events into a ScoreSignals value for
// the parent session. Events flagged as subagent (SessionEvent.IsSubagent())
// are ignored — see AggregateSubagent for the subagent-only view.
func Aggregate(events []contracts.SessionEvent) contracts.ScoreSignals {
	return aggregate(events, false)
}

// AggregateSubagent folds the supplied session events into a ScoreSignals
// value for subagent activity only. Non-subagent events are ignored. This
// preserves the "subagent independent scoring" invariant from
// ARCHITECTURE.md §9 Step 5 — the parent score never absorbs subagent
// signals and vice versa.
func AggregateSubagent(events []contracts.SessionEvent) contracts.ScoreSignals {
	return aggregate(events, true)
}

// aggregate is the shared, pure implementation. wantSubagent selects which
// partition of the input contributes to the result.
func aggregate(events []contracts.SessionEvent, wantSubagent bool) contracts.ScoreSignals {
	if len(events) == 0 {
		return contracts.ScoreSignals{}
	}

	// Dedup by ID. We do not assume any natural ordering; the first occurrence
	// wins (subsequent duplicates are dropped). Using a set keeps the function
	// order-independent: the set of accepted IDs is the same regardless of
	// the input order.
	seen := make(map[string]struct{}, len(events))

	var (
		peer        int
		selfR       int
		turns       int
		overrides   int
		acceptances int
		rejections  int
	)

	for _, e := range events {
		if e.IsSubagent() != wantSubagent {
			continue
		}
		if e.ID != "" {
			if _, dup := seen[e.ID]; dup {
				continue
			}
			seen[e.ID] = struct{}{}
		}

		switch e.Type {
		case contracts.EventPeerReuse:
			peer++
		case contracts.EventSelfReuse:
			selfR++
		case contracts.EventTurnCompleted:
			turns++
		case contracts.EventUserOverride:
			overrides++
		case contracts.EventSuggestionAccepted:
			acceptances++
		case contracts.EventSuggestionRejected:
			rejections++
		}
	}

	out := contracts.ScoreSignals{}

	// peer_reuse_count / self_reuse_count: only surface when we actually saw
	// at least one event of that kind. Zero-count for a session that simply
	// had no reuse events is semantically the same as "no signal"; keeping
	// the pointer nil mirrors ScoreSignals' optional contract and matches
	// Composite()'s treatment of missing-vs-zero (the Composite function
	// reads PeerReuseCount as an int, so the caller can deref when needed).
	if peer > 0 {
		v := peer
		out.PeerReuseCount = &v
	}
	if selfR > 0 {
		v := selfR
		out.SelfReuseCount = &v
	}

	// override_rate is undefined when there are no overrides at all (treat
	// zero as "no signal", per the counters-of-0-surface-as-nil rule) or
	// when there are no completed turns (denominator zero).
	if overrides > 0 && turns > 0 {
		rate := float64(overrides) / float64(turns)
		if rate > 1.0 {
			rate = 1.0
		}
		out.OverrideRate = &rate
	}

	// suggestion_acceptance is undefined when no suggestion outcome events
	// occurred at all. If only rejections occurred, the rate is 0.0 (a real
	// signal, not "missing").
	if acceptances+rejections > 0 {
		rate := float64(acceptances) / float64(acceptances+rejections)
		out.SuggestionAcceptance = &rate
	}

	return out
}
