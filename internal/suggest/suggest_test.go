package suggest_test

import (
	"math"
	"testing"

	"github.com/iter-dev/iter/internal/suggest"
	"github.com/iter-dev/iter/pkg/contracts"
)

// ---------------------------------------------------------------------------
// Boundary tests at every locked threshold point.
//
// Locked contract (contracts.py:86-87, ARCHITECTURE.md §5):
//   confidence <  0.50  → Suppress
//   confidence <  0.80  → Advisory
//   confidence >= 0.80  → Replace
// ---------------------------------------------------------------------------

func TestSuggestionAction_Boundaries(t *testing.T) {
	const refined = "rewrite as: ..."

	cases := []struct {
		name       string
		confidence float64
		wantAction contracts.Action
		wantPrompt string
	}{
		{
			name:       "zero confidence suppresses",
			confidence: 0.0,
			wantAction: contracts.ActionSuppress,
			wantPrompt: "",
		},
		{
			name:       "just below suppress boundary suppresses",
			confidence: 0.4999,
			wantAction: contracts.ActionSuppress,
			wantPrompt: "",
		},
		{
			name:       "exactly at suppress boundary is advisory",
			confidence: 0.50,
			wantAction: contracts.ActionAdvisory,
			wantPrompt: refined,
		},
		{
			name:       "just below replace boundary is advisory",
			confidence: 0.7999,
			wantAction: contracts.ActionAdvisory,
			wantPrompt: refined,
		},
		{
			name:       "exactly at replace boundary replaces",
			confidence: 0.80,
			wantAction: contracts.ActionReplace,
			wantPrompt: refined,
		},
		{
			name:       "one replaces",
			confidence: 1.0,
			wantAction: contracts.ActionReplace,
			wantPrompt: refined,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAction, gotPrompt := suggest.SuggestionAction(tc.confidence, refined)
			if gotAction != tc.wantAction {
				t.Fatalf("action: got %q, want %q", gotAction, tc.wantAction)
			}
			if gotPrompt != tc.wantPrompt {
				t.Fatalf("prompt: got %q, want %q", gotPrompt, tc.wantPrompt)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Out-of-band inputs.
//
// Policy (recorded in DECISIONS.md, Phase 5 "Suggest-side input policy"):
//   NaN          → Suppress, empty prompt
//   confidence < 0   → Suppress, empty prompt (treated as 0 ⇒ suppress)
//   confidence > 1   → Replace, refined prompt (clamped to 1 ⇒ replace)
//
// Rationale: the suggest path is a hot, latency-sensitive request. We never
// return an error — we degrade gracefully to the safest action. NaN and
// negative are mapped to the safest user-facing outcome (don't surface a
// possibly-bad suggestion); >1 is treated as max confidence (clamp to 1).
// ---------------------------------------------------------------------------

func TestSuggestionAction_NaNSuppresses(t *testing.T) {
	got, prompt := suggest.SuggestionAction(math.NaN(), "anything")
	if got != contracts.ActionSuppress {
		t.Fatalf("NaN should Suppress, got %q", got)
	}
	if prompt != "" {
		t.Fatalf("NaN should return empty prompt, got %q", prompt)
	}
}

func TestSuggestionAction_NegativeSuppresses(t *testing.T) {
	for _, c := range []float64{-0.0001, -1.0, -1e9, math.Inf(-1)} {
		got, prompt := suggest.SuggestionAction(c, "anything")
		if got != contracts.ActionSuppress {
			t.Fatalf("confidence=%v should Suppress, got %q", c, got)
		}
		if prompt != "" {
			t.Fatalf("confidence=%v should return empty prompt, got %q", c, prompt)
		}
	}
}

func TestSuggestionAction_AboveOneReplaces(t *testing.T) {
	const refined = "do the better thing"
	for _, c := range []float64{1.0001, 2.0, 1e9, math.Inf(+1)} {
		got, prompt := suggest.SuggestionAction(c, refined)
		if got != contracts.ActionReplace {
			t.Fatalf("confidence=%v should Replace, got %q", c, got)
		}
		if prompt != refined {
			t.Fatalf("confidence=%v should echo refined prompt, got %q", c, prompt)
		}
	}
}

// Determinism: same inputs → same outputs, no clocks or globals.
func TestSuggestionAction_Deterministic(t *testing.T) {
	a1, p1 := suggest.SuggestionAction(0.65, "x")
	a2, p2 := suggest.SuggestionAction(0.65, "x")
	if a1 != a2 || p1 != p2 {
		t.Fatalf("non-deterministic: (%q,%q) vs (%q,%q)", a1, p1, a2, p2)
	}
}

// Advisory and Replace must echo the refined prompt verbatim — never edit it.
func TestSuggestionAction_EchoesRefinedPromptVerbatim(t *testing.T) {
	const refined = "  weird   whitespace\n\tand unicode: café 🚀  "
	_, p := suggest.SuggestionAction(0.6, refined)
	if p != refined {
		t.Fatalf("advisory should echo verbatim, got %q", p)
	}
	_, p = suggest.SuggestionAction(0.9, refined)
	if p != refined {
		t.Fatalf("replace should echo verbatim, got %q", p)
	}
}
