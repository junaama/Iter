package suggest

import (
	"math"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Locked confidence thresholds. These are the ONLY place in the Go codebase
// where the numeric thresholds live; the literals_test.go scanner enforces
// that no other file under internal/ reintroduces them. They mirror the
// Python originals in contracts.py:86-87
// (CONFIDENCE_SUPPRESS_BELOW / CONFIDENCE_REPLACE_AT).
//
// See ARCHITECTURE.md §5 and CLAUDE.md "Locked invariants".
const (
	// confidenceSuppressBelow: strictly below this value → ActionSuppress.
	confidenceSuppressBelow = 0.50
	// confidenceReplaceAt: at or above this value → ActionReplace.
	confidenceReplaceAt = 0.80
)

// SuggestionAction is the pure decision function called by the CLI, daemon,
// and dashboard to turn a (confidence, refined_prompt) pair into the action
// the caller should take. Same inputs → same outputs; no I/O, no clocks, no
// globals.
//
// Contract (locked; see CLAUDE.md "Locked invariants"):
//
//	confidence <  0.50  → ActionSuppress, ""
//	confidence <  0.80  → ActionAdvisory, refinedPrompt
//	confidence >= 0.80  → ActionReplace,  refinedPrompt
//
// Out-of-band input policy (recorded in DECISIONS.md, Phase 5 "Suggest-side
// input policy"):
//
//	NaN              → ActionSuppress, "" (degrade safe; never surface)
//	confidence < 0   → ActionSuppress, "" (treated as 0)
//	confidence > 1   → ActionReplace,  refinedPrompt (clamped to 1)
//
// The suggest path is latency-critical, so this function never returns an
// error. The composite scorer (internal/scoring) is the source of truth for
// confidence; if it ever emits a value outside [0,1] this function still
// produces a well-defined action without blocking the request.
func SuggestionAction(confidence float64, refinedPrompt string) (contracts.Action, string) {
	// NaN and negatives degrade to the safest user-facing outcome.
	if math.IsNaN(confidence) || confidence < 0 {
		return contracts.ActionSuppress, ""
	}

	if confidence < confidenceSuppressBelow {
		return contracts.ActionSuppress, ""
	}
	if confidence < confidenceReplaceAt {
		return contracts.ActionAdvisory, refinedPrompt
	}
	// Includes confidence > 1 (clamped to Replace) and +Inf.
	return contracts.ActionReplace, refinedPrompt
}
