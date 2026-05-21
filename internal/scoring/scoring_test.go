package scoring_test

import (
	"math"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/iter-dev/iter/internal/scoring"
	"github.com/iter-dev/iter/pkg/contracts"
)

func f64p(v float64) *float64 { return &v }

const eps = 1e-9

// ---------------------------------------------------------------------------
// Table-driven tests: hand-curated (inputs → expected composite_score)
// ---------------------------------------------------------------------------

func TestComposite_Table(t *testing.T) {
	cases := []struct {
		name    string
		in      contracts.CompositeScoreInputs
		want    float64
		wantTol float64
	}{
		{
			name:    "zero-value: no signals → score 0",
			in:      contracts.CompositeScoreInputs{},
			want:    0.0,
			wantTol: eps,
		},
		{
			name: "max signals → score 1",
			in: contracts.CompositeScoreInputs{
				Durability7d:         f64p(1.0),
				Durability30d:        f64p(1.0),
				PeerReuseCount:       10000,
				SelfReuseCount:       10000,
				OverrideRate:         f64p(0.0),
				SuggestionAcceptance: f64p(1.0),
			},
			want:    1.0,
			wantTol: 1e-3, // saturate(1e4, 3) is extremely close to but not exactly 1
		},
		{
			name: "min signals → score 0",
			in: contracts.CompositeScoreInputs{
				Durability7d:         f64p(0.0),
				Durability30d:        f64p(0.0),
				PeerReuseCount:       0,
				SelfReuseCount:       0,
				OverrideRate:         f64p(1.0),
				SuggestionAcceptance: f64p(0.0),
			},
			want:    0.0,
			wantTol: eps,
		},
		{
			name: "only durability_7d=0.8",
			in: contracts.CompositeScoreInputs{
				Durability7d: f64p(0.8),
			},
			// weights present: durability_7d (0.25) + peer (0.20) + self (0.10) = 0.55
			// numerator: 0.25 * 0.8 + 0 + 0 = 0.2
			// composite = 0.2 / 0.55
			want:    0.2 / 0.55,
			wantTol: eps,
		},
		{
			name: "only override_rate=0.5",
			in: contracts.CompositeScoreInputs{
				OverrideRate: f64p(0.5),
			},
			// weights present: override (0.10) + peer (0.20) + self (0.10) = 0.40
			// numerator: 0.10 * (1 - 0.5) = 0.05
			want:    0.05 / 0.40,
			wantTol: eps,
		},
		{
			name: "high suggestion_acceptance and peer_reuse",
			in: contracts.CompositeScoreInputs{
				PeerReuseCount:       5,
				SuggestionAcceptance: f64p(0.9),
			},
			// weights: peer (0.20) + self (0.10) + suggestion (0.20) = 0.50
			// peer value = 1 - exp(-5/3) ≈ 0.8111
			// numerator: 0.20 * 0.8111 + 0 + 0.20 * 0.9
			want:    (0.20*(1.0-math.Exp(-5.0/3.0)) + 0.20*0.9) / 0.50,
			wantTol: 1e-6,
		},
		{
			name: "negative reuse counts clamped to zero",
			in: contracts.CompositeScoreInputs{
				PeerReuseCount: -42,
				SelfReuseCount: -1,
			},
			// peer=0, self=0; weights = 0.30; numerator = 0
			want:    0.0,
			wantTol: eps,
		},
		{
			name: "out-of-range durability_7d > 1 clamped to 1",
			in: contracts.CompositeScoreInputs{
				Durability7d: f64p(5.0),
			},
			// clamp(5.0) = 1.0
			// weights: durability_7d (0.25) + peer (0.20) + self (0.10) = 0.55
			// numerator: 0.25
			want:    0.25 / 0.55,
			wantTol: eps,
		},
		{
			name: "out-of-range durability_7d < 0 clamped to 0",
			in: contracts.CompositeScoreInputs{
				Durability7d: f64p(-1.0),
			},
			want:    0.0,
			wantTol: eps,
		},
		{
			name: "NaN durability_7d treated as missing",
			in: contracts.CompositeScoreInputs{
				Durability7d:  f64p(math.NaN()),
				Durability30d: f64p(0.8),
			},
			// weights present: durability_30d (0.15) + peer (0.20) + self (0.10) = 0.45
			// numerator: 0.15 * 0.8
			want:    0.15 * 0.8 / 0.45,
			wantTol: eps,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := scoring.Composite(tc.in)
			if math.Abs(out.CompositeScore-tc.want) > tc.wantTol {
				t.Errorf("composite = %.9f, want %.9f (±%g)", out.CompositeScore, tc.want, tc.wantTol)
			}
			if out.Rationale == "" {
				t.Error("rationale must not be empty")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Boundedness: score always in [0, 1] for arbitrary inputs.
// ---------------------------------------------------------------------------

func TestComposite_Bounded(t *testing.T) {
	f := func(d7, d30, ovr, sug float64, peer, self int, weight float64) bool {
		in := contracts.CompositeScoreInputs{
			Durability7d:         &d7,
			Durability30d:        &d30,
			PeerReuseCount:       peer,
			SelfReuseCount:       self,
			OverrideRate:         &ovr,
			SuggestionAcceptance: &sug,
			ContributorWeight:    weight,
		}
		out := scoring.Composite(in)
		return out.CompositeScore >= 0.0 && out.CompositeScore <= 1.0 && !math.IsNaN(out.CompositeScore)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 2000}); err != nil {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// Determinism: same inputs → same output, every time.
// ---------------------------------------------------------------------------

func TestComposite_Determinism(t *testing.T) {
	in := contracts.CompositeScoreInputs{
		Durability7d:         f64p(0.42),
		Durability30d:        f64p(0.31),
		PeerReuseCount:       7,
		SelfReuseCount:       3,
		OverrideRate:         f64p(0.15),
		SuggestionAcceptance: f64p(0.66),
	}
	first := scoring.Composite(in)
	for i := 0; i < 100; i++ {
		out := scoring.Composite(in)
		if out.CompositeScore != first.CompositeScore {
			t.Fatalf("run %d: composite changed from %.17g to %.17g", i, first.CompositeScore, out.CompositeScore)
		}
		if out.Rationale != first.Rationale {
			t.Fatalf("run %d: rationale changed", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Monotonicity: increasing any *positive* signal (with all else equal) must
// never DECREASE the composite. The issue's locked invariant.
// ---------------------------------------------------------------------------

func TestComposite_Monotonic_Durability7d(t *testing.T) {
	f := func(d30, ovr, sug, lo, hi float64, peer, self int) bool {
		lo = clamp01ForTest(lo)
		hi = clamp01ForTest(hi)
		if lo > hi {
			lo, hi = hi, lo
		}
		base := contracts.CompositeScoreInputs{
			Durability30d:        &d30,
			PeerReuseCount:       peer,
			SelfReuseCount:       self,
			OverrideRate:         &ovr,
			SuggestionAcceptance: &sug,
		}
		baseLow := base
		baseLow.Durability7d = &lo
		baseHigh := base
		baseHigh.Durability7d = &hi
		return scoring.Composite(baseHigh).CompositeScore+1e-12 >= scoring.Composite(baseLow).CompositeScore
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

func TestComposite_Monotonic_Durability30d(t *testing.T) {
	f := func(d7, ovr, sug, lo, hi float64, peer, self int) bool {
		lo = clamp01ForTest(lo)
		hi = clamp01ForTest(hi)
		if lo > hi {
			lo, hi = hi, lo
		}
		base := contracts.CompositeScoreInputs{
			Durability7d:         &d7,
			PeerReuseCount:       peer,
			SelfReuseCount:       self,
			OverrideRate:         &ovr,
			SuggestionAcceptance: &sug,
		}
		baseLow := base
		baseLow.Durability30d = &lo
		baseHigh := base
		baseHigh.Durability30d = &hi
		return scoring.Composite(baseHigh).CompositeScore+1e-12 >= scoring.Composite(baseLow).CompositeScore
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

func TestComposite_Monotonic_SuggestionAcceptance(t *testing.T) {
	f := func(d7, d30, ovr, lo, hi float64, peer, self int) bool {
		lo = clamp01ForTest(lo)
		hi = clamp01ForTest(hi)
		if lo > hi {
			lo, hi = hi, lo
		}
		base := contracts.CompositeScoreInputs{
			Durability7d:   &d7,
			Durability30d:  &d30,
			PeerReuseCount: peer,
			SelfReuseCount: self,
			OverrideRate:   &ovr,
		}
		baseLow := base
		baseLow.SuggestionAcceptance = &lo
		baseHigh := base
		baseHigh.SuggestionAcceptance = &hi
		return scoring.Composite(baseHigh).CompositeScore+1e-12 >= scoring.Composite(baseLow).CompositeScore
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

func TestComposite_Monotonic_PeerReuseCount(t *testing.T) {
	f := func(d7, d30, ovr, sug float64, self, lo, hi int) bool {
		if lo < 0 {
			lo = -lo
		}
		if hi < 0 {
			hi = -hi
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		base := contracts.CompositeScoreInputs{
			Durability7d:         &d7,
			Durability30d:        &d30,
			SelfReuseCount:       self,
			OverrideRate:         &ovr,
			SuggestionAcceptance: &sug,
		}
		baseLow := base
		baseLow.PeerReuseCount = lo
		baseHigh := base
		baseHigh.PeerReuseCount = hi
		return scoring.Composite(baseHigh).CompositeScore+1e-12 >= scoring.Composite(baseLow).CompositeScore
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

func TestComposite_Monotonic_SelfReuseCount(t *testing.T) {
	f := func(d7, d30, ovr, sug float64, peer, lo, hi int) bool {
		if lo < 0 {
			lo = -lo
		}
		if hi < 0 {
			hi = -hi
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		base := contracts.CompositeScoreInputs{
			Durability7d:         &d7,
			Durability30d:        &d30,
			PeerReuseCount:       peer,
			OverrideRate:         &ovr,
			SuggestionAcceptance: &sug,
		}
		baseLow := base
		baseLow.SelfReuseCount = lo
		baseHigh := base
		baseHigh.SelfReuseCount = hi
		return scoring.Composite(baseHigh).CompositeScore+1e-12 >= scoring.Composite(baseLow).CompositeScore
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

// override_rate is a *negative* signal — increasing it must NOT increase the score.
func TestComposite_OverrideRate_AntiMonotonic(t *testing.T) {
	f := func(d7, d30, sug, lo, hi float64, peer, self int) bool {
		lo = clamp01ForTest(lo)
		hi = clamp01ForTest(hi)
		if lo > hi {
			lo, hi = hi, lo
		}
		base := contracts.CompositeScoreInputs{
			Durability7d:         &d7,
			Durability30d:        &d30,
			PeerReuseCount:       peer,
			SelfReuseCount:       self,
			SuggestionAcceptance: &sug,
		}
		baseLow := base
		baseLow.OverrideRate = &lo
		baseHigh := base
		baseHigh.OverrideRate = &hi
		return scoring.Composite(baseLow).CompositeScore+1e-12 >= scoring.Composite(baseHigh).CompositeScore
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// Ordering / shuffle independence.
//
// CompositeScoreInputs has no list-shaped fields at v1, so this property is
// vacuously satisfied at the input boundary. The test makes that explicit by
// asserting that constructing the same input via field-order-agnostic copies
// produces identical output — guarding against any future regression that
// might smuggle in order-sensitive behavior.
// ---------------------------------------------------------------------------

func TestComposite_OrderingIndependence_NoListInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		d7 := rng.Float64()
		d30 := rng.Float64()
		ovr := rng.Float64()
		sug := rng.Float64()
		peer := rng.Intn(50)
		self := rng.Intn(50)

		in1 := contracts.CompositeScoreInputs{
			Durability7d:         &d7,
			Durability30d:        &d30,
			PeerReuseCount:       peer,
			SelfReuseCount:       self,
			OverrideRate:         &ovr,
			SuggestionAcceptance: &sug,
		}
		// Same fields, assembled in a different statement ordering / pointer copies.
		var in2 contracts.CompositeScoreInputs
		in2.SuggestionAcceptance = &sug
		in2.OverrideRate = &ovr
		in2.SelfReuseCount = self
		in2.PeerReuseCount = peer
		in2.Durability30d = &d30
		in2.Durability7d = &d7

		a := scoring.Composite(in1)
		b := scoring.Composite(in2)
		if a.CompositeScore != b.CompositeScore {
			t.Fatalf("iter %d: ordering-different assembly produced different scores: %v vs %v", i, a.CompositeScore, b.CompositeScore)
		}
	}
}

// ---------------------------------------------------------------------------
// Output echo: signals_used echoes the input signals (excluding fields that
// aren't part of ScoreSignals like wall_time_ms / contributor_weight).
// ---------------------------------------------------------------------------

func TestComposite_SignalsUsed_EchoesInputs(t *testing.T) {
	in := contracts.CompositeScoreInputs{
		Durability7d:         f64p(0.5),
		Durability30d:        f64p(0.6),
		PeerReuseCount:       4,
		SelfReuseCount:       2,
		OverrideRate:         f64p(0.1),
		SuggestionAcceptance: f64p(0.7),
	}
	out := scoring.Composite(in)
	if out.SignalsUsed.Durability7d == nil || *out.SignalsUsed.Durability7d != 0.5 {
		t.Error("durability_7d not echoed")
	}
	if out.SignalsUsed.PeerReuseCount == nil || *out.SignalsUsed.PeerReuseCount != 4 {
		t.Error("peer_reuse_count not echoed")
	}
	if out.SignalsUsed.OverrideRate == nil || *out.SignalsUsed.OverrideRate != 0.1 {
		t.Error("override_rate not echoed")
	}
}

func TestComposite_SignalsUsed_MissingInputsAreNilEchoed(t *testing.T) {
	in := contracts.CompositeScoreInputs{
		PeerReuseCount: 5,
	}
	out := scoring.Composite(in)
	if out.SignalsUsed.Durability7d != nil {
		t.Error("expected durability_7d to be nil-echoed when input was nil")
	}
	if out.SignalsUsed.OverrideRate != nil {
		t.Error("expected override_rate to be nil-echoed when input was nil")
	}
	if out.SignalsUsed.PeerReuseCount == nil || *out.SignalsUsed.PeerReuseCount != 5 {
		t.Error("peer_reuse_count should be echoed")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func clamp01ForTest(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
