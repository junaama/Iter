package scoring

import (
	"fmt"
	"math"
	"strings"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Per-signal weights for the v1 composite score. Weights are renormalized
// over the subset of signals actually present on a given input, so the score
// stays well-defined when later-added signals are missing on older data.
//
// peerReuseSat / selfReuseSat are the saturation constants k in the transform
// reuseValue(n) = 1 - exp(-n/k). Higher k → reuse counts grow more slowly
// toward 1; lower k → saturate sooner.
const (
	wDurability7d         = 0.25
	wDurability30d        = 0.15
	wPeerReuse            = 0.20
	wSelfReuse            = 0.10
	wOverrideRate         = 0.10
	wSuggestionAcceptance = 0.20

	peerReuseSat = 3.0
	selfReuseSat = 3.0
)

// Composite computes a deterministic composite score from the supplied
// signals. The function is pure: it performs no I/O, reads no clocks, and
// does not consult any globals.
//
// Contract:
//   - The returned CompositeScore is always in [0.0, 1.0].
//   - Missing signals (nil pointers) are excluded from both the numerator
//     and the denominator; weights are renormalized over what is present.
//   - peer_reuse_count and self_reuse_count are always considered present
//     (the input fields are non-pointer ints). Negative values are clamped
//     to zero.
//   - Floats are clamped to [0,1] before scoring. NaN is treated as missing.
//   - When no signal contributes any weight, the score is exactly 0.0.
//   - The wall_time_ms, turn_count, and contributor_weight inputs are not
//     used by the composite at v1; they are accepted for forward-compat with
//     contracts.py CompositeScoreInputs but do not affect the score.
func Composite(in contracts.CompositeScoreInputs) contracts.CompositeScoreOutput {
	var num, den float64
	var contributing []string

	if v, ok := normalizeUnit(in.Durability7d); ok {
		num += wDurability7d * v
		den += wDurability7d
		contributing = append(contributing, "durability_7d")
	}
	if v, ok := normalizeUnit(in.Durability30d); ok {
		num += wDurability30d * v
		den += wDurability30d
		contributing = append(contributing, "durability_30d")
	}

	peer := saturate(nonNegativeInt(in.PeerReuseCount), peerReuseSat)
	num += wPeerReuse * peer
	den += wPeerReuse
	contributing = append(contributing, "peer_reuse_count")

	self := saturate(nonNegativeInt(in.SelfReuseCount), selfReuseSat)
	num += wSelfReuse * self
	den += wSelfReuse
	contributing = append(contributing, "self_reuse_count")

	if v, ok := normalizeUnit(in.OverrideRate); ok {
		num += wOverrideRate * (1.0 - v)
		den += wOverrideRate
		contributing = append(contributing, "override_rate")
	}
	if v, ok := normalizeUnit(in.SuggestionAcceptance); ok {
		num += wSuggestionAcceptance * v
		den += wSuggestionAcceptance
		contributing = append(contributing, "suggestion_acceptance")
	}

	var composite float64
	if den > 0 {
		composite = num / den
	}
	composite = clamp01(composite)

	return contracts.CompositeScoreOutput{
		CompositeScore: composite,
		SignalsUsed:    echoSignals(in),
		Rationale: fmt.Sprintf(
			"composite=%.4f over %d signal(s): %s",
			composite, len(contributing), strings.Join(contributing, ", "),
		),
	}
}

// normalizeUnit clamps the value to [0,1] and returns (value, true) when the
// pointer is non-nil and not NaN. NaN is treated as a missing signal.
func normalizeUnit(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	v := *p
	if math.IsNaN(v) {
		return 0, false
	}
	return clamp01(v), true
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func nonNegativeInt(n int) float64 {
	if n < 0 {
		return 0
	}
	return float64(n)
}

// saturate maps a non-negative count to [0, 1) via 1 - exp(-x/k).
// Strictly monotonic non-decreasing in x; equal-or-greater inputs always
// yield equal-or-greater outputs (required by the monotonicity invariant).
func saturate(x, k float64) float64 {
	if x <= 0 {
		return 0
	}
	return 1.0 - math.Exp(-x/k)
}

// echoSignals copies the input signal fields into a ScoreSignals struct for
// the output. Pointer fields are reused (immutable from the caller's POV
// because Composite never writes through them).
func echoSignals(in contracts.CompositeScoreInputs) contracts.ScoreSignals {
	peer := in.PeerReuseCount
	self := in.SelfReuseCount
	return contracts.ScoreSignals{
		Durability7d:         in.Durability7d,
		Durability30d:        in.Durability30d,
		PeerReuseCount:       &peer,
		SelfReuseCount:       &self,
		OverrideRate:         in.OverrideRate,
		SuggestionAcceptance: in.SuggestionAcceptance,
	}
}
