package contracts

import (
	"time"

	"github.com/google/uuid"
)

// Harness mirrors contracts.py Harness. Values are wire-visible because
// clients use them in filters and summary rows.
type Harness string

const (
	HarnessClaudeCode Harness = "claude_code"
	HarnessCodex      Harness = "codex"
	HarnessGeminiCLI  Harness = "gemini_cli"
	HarnessOpenCode   Harness = "opencode"
	HarnessPi         Harness = "pi"
)

// ValidHarness reports whether v is one of the v1 supported harness tokens.
func ValidHarness(v string) bool {
	switch Harness(v) {
	case HarnessClaudeCode, HarnessCodex, HarnessGeminiCLI, HarnessOpenCode, HarnessPi:
		return true
	default:
		return false
	}
}

// Effort mirrors contracts.py Effort.
type Effort string

const (
	EffortLow   Effort = "low"
	EffortMed   Effort = "med"
	EffortHigh  Effort = "high"
	EffortXHigh Effort = "xhigh"
	EffortMax   Effort = "max"
)

// Classification mirrors the three-tier redaction classification.
type Classification string

const (
	ClassificationClean      Classification = "clean"
	ClassificationStrippable Classification = "strippable"
	ClassificationDirty      Classification = "dirty"
)

// ValidClassification reports whether v is one of the v1 classification tokens.
func ValidClassification(v string) bool {
	switch Classification(v) {
	case ClassificationClean, ClassificationStrippable, ClassificationDirty:
		return true
	default:
		return false
	}
}

// OutcomeType mirrors contracts.py OutcomeType and outcomes.outcome_type.
type OutcomeType string

const (
	OutcomeCommitLanded         OutcomeType = "commit_landed"
	OutcomePRMerged             OutcomeType = "pr_merged"
	OutcomePRReverted           OutcomeType = "pr_reverted"
	OutcomeCodeRevertedWithin7d OutcomeType = "code_reverted_within_7d"
	OutcomeCodeRevertedWithin7  OutcomeType = OutcomeCodeRevertedWithin7d
	OutcomeTestsPassed          OutcomeType = "tests_passed"
	OutcomeTestsFailed          OutcomeType = "tests_failed"
	OutcomeIncidentCaused       OutcomeType = "incident_caused"
	OutcomePeerReferenced       OutcomeType = "peer_referenced"
)

// ValidOutcomeType reports whether v is one of the v1 outcome tokens.
func ValidOutcomeType(v string) bool {
	switch OutcomeType(v) {
	case OutcomeCommitLanded, OutcomePRMerged, OutcomePRReverted,
		OutcomeCodeRevertedWithin7d, OutcomeTestsPassed, OutcomeTestsFailed,
		OutcomeIncidentCaused, OutcomePeerReferenced:
		return true
	default:
		return false
	}
}

// SessionScoreView mirrors contracts.py SessionScoreView.
type SessionScoreView struct {
	SessionID         uuid.UUID    `json:"session_id"`
	CompositeScore    float64      `json:"composite_score"`
	Signals           ScoreSignals `json:"signals"`
	ContributorWeight float64      `json:"contributor_weight"`
	ScoredAt          time.Time    `json:"scored_at"`
	Rationale         *string      `json:"rationale"`
}

// SessionSummary mirrors contracts.py SessionSummary and is reused by
// dashboard recent_sessions and GET /v1/sessions.
type SessionSummary struct {
	ID              uuid.UUID         `json:"id"`
	UserID          uuid.UUID         `json:"user_id"`
	ParentSessionID *uuid.UUID        `json:"parent_session_id"`
	Harness         Harness           `json:"harness"`
	Model           string            `json:"model"`
	Effort          *Effort           `json:"effort"`
	Tools           []string          `json:"tools"`
	StartedAt       time.Time         `json:"started_at"`
	EndedAt         *time.Time        `json:"ended_at"`
	WallTimeMs      *int32            `json:"wall_time_ms"`
	TurnCount       *int32            `json:"turn_count"`
	RedactedPrompt  string            `json:"redacted_prompt"`
	LatestScore     *SessionScoreView `json:"latest_score,omitempty"`
}

// ListSessionsResponse is the GET /v1/sessions response envelope.
type ListSessionsResponse struct {
	Sessions   []SessionSummary `json:"sessions"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}
