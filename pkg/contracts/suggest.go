package contracts

// Action is the decision returned by the pure suggestion_action function in
// internal/suggest. The wire-level enum is locked: the daemon, CLI, and
// dashboard all key off these three values to decide what to do with a
// refined prompt at task-start.
//
// Locked thresholds (see contracts.py CONFIDENCE_SUPPRESS_BELOW /
// CONFIDENCE_REPLACE_AT, ARCHITECTURE.md §5, CLAUDE.md "Locked invariants"):
//
//	confidence <  0.50  → ActionSuppress
//	confidence <  0.80  → ActionAdvisory
//	confidence >= 0.80  → ActionReplace
//
// The string values match the lowercase tokens used in contracts.py-side
// fixtures and the dashboard UI state, so JSON marshaling is direct.
type Action string

const (
	// ActionSuppress means the suggestion should not be surfaced to the user.
	ActionSuppress Action = "suppress"
	// ActionAdvisory means the suggestion is surfaced as advisory; the user
	// keeps their original prompt unless they choose otherwise.
	ActionAdvisory Action = "advisory"
	// ActionReplace means the suggestion is offered as a clipboard-ready
	// replacement for the user's prompt. Per CLAUDE.md "Locked invariants",
	// the suggestion is never injected into a terminal directly.
	ActionReplace Action = "replace"
)
