// Package contracts mirrors contracts.py wire types in Go. contracts.py is
// canonical until the Go server lands; this package exists so internal/* can
// type-check against the same shapes.
package contracts

import (
	"encoding/json"
)

// ScoreSignals mirrors contracts.py ScoreSignals. Signals evolve, so unknown
// JSON fields are preserved in Extra (matching the Python `extra="allow"`).
// This is the structure persisted to session_scores.signals (jsonb).
type ScoreSignals struct {
	Durability7d         *float64 `json:"durability_7d,omitempty"`
	Durability30d        *float64 `json:"durability_30d,omitempty"`
	PeerReuseCount       *int     `json:"peer_reuse_count,omitempty"`
	SelfReuseCount       *int     `json:"self_reuse_count,omitempty"`
	OverrideRate         *float64 `json:"override_rate,omitempty"`
	SuggestionAcceptance *float64 `json:"suggestion_acceptance,omitempty"`

	// Extra captures forward-compatible signal fields not declared above.
	Extra map[string]json.RawMessage `json:"-"`
}

var scoreSignalsKnownFields = map[string]struct{}{
	"durability_7d":         {},
	"durability_30d":        {},
	"peer_reuse_count":      {},
	"self_reuse_count":      {},
	"override_rate":         {},
	"suggestion_acceptance": {},
}

// UnmarshalJSON accepts unknown fields and stores them in Extra.
func (s *ScoreSignals) UnmarshalJSON(data []byte) error {
	type alias ScoreSignals
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*s = ScoreSignals(tmp)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		if _, known := scoreSignalsKnownFields[k]; known {
			continue
		}
		if s.Extra == nil {
			s.Extra = make(map[string]json.RawMessage)
		}
		s.Extra[k] = v
	}
	return nil
}

// CompositeScoreInputs mirrors contracts.py CompositeScoreInputs (extra="forbid").
// Pure inputs to the scoring function: same inputs → same score.
type CompositeScoreInputs struct {
	Durability7d         *float64 `json:"durability_7d,omitempty"`
	Durability30d        *float64 `json:"durability_30d,omitempty"`
	PeerReuseCount       int      `json:"peer_reuse_count"`
	SelfReuseCount       int      `json:"self_reuse_count"`
	OverrideRate         *float64 `json:"override_rate,omitempty"`
	SuggestionAcceptance *float64 `json:"suggestion_acceptance,omitempty"`
	WallTimeMs           *int     `json:"wall_time_ms,omitempty"`
	TurnCount            *int     `json:"turn_count,omitempty"`
	ContributorWeight    float64  `json:"contributor_weight"`
}

// CompositeScoreOutput mirrors contracts.py CompositeScoreOutput. composite_score
// is constrained to [0.0, 1.0] by the scoring implementation.
type CompositeScoreOutput struct {
	CompositeScore float64      `json:"composite_score"`
	SignalsUsed    ScoreSignals `json:"signals_used"`
	Rationale      string       `json:"rationale"`
}
