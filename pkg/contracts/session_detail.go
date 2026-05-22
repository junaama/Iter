package contracts

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SessionDetail is the dashboard/session-detail projection of one
// sessions row. It intentionally includes every sessions column so the
// SwiftUI detail screen can render audit/debug metadata without another
// endpoint.
type SessionDetail struct {
	ID              uuid.UUID      `json:"id"`
	TenantID        uuid.UUID      `json:"tenant_id"`
	UserID          uuid.UUID      `json:"user_id"`
	ParentSessionID *uuid.UUID     `json:"parent_session_id,omitempty"`
	Harness         Harness        `json:"harness"`
	Model           string         `json:"model"`
	Effort          *Effort        `json:"effort,omitempty"`
	Tools           []string       `json:"tools"`
	RepoHash        *string        `json:"repo_hash,omitempty"`
	GitBranch       *string        `json:"git_branch,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	EndedAt         *time.Time     `json:"ended_at,omitempty"`
	WallTimeMs      *int32         `json:"wall_time_ms,omitempty"`
	TurnCount       *int32         `json:"turn_count,omitempty"`
	TotalTokensIn   *int64         `json:"total_tokens_in,omitempty"`
	TotalTokensOut  *int64         `json:"total_tokens_out,omitempty"`
	RedactedPrompt  string         `json:"redacted_prompt"`
	RedactedSystem  *string        `json:"redacted_system,omitempty"`
	Classification  Classification `json:"classification"`
	IngestedAt      time.Time      `json:"ingested_at"`
	ArchivedAt      *time.Time     `json:"archived_at,omitempty"`
}

// SessionEventDetail is one persisted session_events row as returned by
// GET /v1/sessions/:id. Payload is intentionally opaque JSON.
type SessionEventDetail struct {
	ID         int64          `json:"id"`
	SessionID  uuid.UUID      `json:"session_id"`
	TenantID   uuid.UUID      `json:"tenant_id"`
	Type       EventType      `json:"event_type"`
	Payload    map[string]any `json:"payload"`
	OccurredAt time.Time      `json:"occurred_at"`
}

// SessionEventsPage wraps the capped event page. next_cursor is omitted
// when the page is complete.
type SessionEventsPage struct {
	Items      []SessionEventDetail `json:"items"`
	NextCursor *string              `json:"next_cursor,omitempty"`
}

// SubagentSessionNode is a recursive tree node for child sessions.
// Depth starts at 1 for direct children of the requested root session.
type SubagentSessionNode struct {
	Session  SessionDetail         `json:"session"`
	Depth    int                   `json:"depth"`
	Children []SubagentSessionNode `json:"children"`
}

// SubagentTree carries the recursive child-session tree and whether
// deeper descendants were intentionally omitted by the depth cap.
type SubagentTree struct {
	Items     []SubagentSessionNode `json:"items"`
	Truncated bool                  `json:"truncated"`
}

// SessionOutcomeDetail is one outcomes row attached to the session.
// details remains raw JSON because webhook senders are source-specific.
type SessionOutcomeDetail struct {
	ID          uuid.UUID       `json:"id"`
	SessionID   uuid.UUID       `json:"session_id"`
	TenantID    uuid.UUID       `json:"tenant_id"`
	OutcomeType OutcomeType     `json:"outcome_type"`
	ExternalRef *string         `json:"external_ref,omitempty"`
	Details     json.RawMessage `json:"details"`
	ObservedAt  time.Time       `json:"observed_at"`
}

// SessionDetailResponse is the composite envelope returned by
// GET /v1/sessions/:id. Outcomes are a sibling array rather than joined
// onto the session row so one-to-many downstream events stay explicit.
type SessionDetailResponse struct {
	Session   SessionDetail          `json:"session"`
	Events    SessionEventsPage      `json:"events"`
	Subagents SubagentTree           `json:"subagents"`
	Outcomes  []SessionOutcomeDetail `json:"outcomes"`
}

// SessionScoreDetail is one persisted scoring run for a session.
type SessionScoreDetail struct {
	ID                uuid.UUID    `json:"id"`
	SessionID         uuid.UUID    `json:"session_id"`
	TenantID          uuid.UUID    `json:"tenant_id"`
	ScorerVersion     string       `json:"scorer_version"`
	CompositeScore    float64      `json:"composite_score"`
	Signals           ScoreSignals `json:"signals"`
	Rationale         *string      `json:"rationale,omitempty"`
	ContributorWeight float64      `json:"contributor_weight"`
	ScoredAt          time.Time    `json:"scored_at"`
}

// SessionScoresResponse is returned by GET /v1/scores/:session_id.
type SessionScoresResponse struct {
	SessionID uuid.UUID            `json:"session_id"`
	Scores    []SessionScoreDetail `json:"scores"`
}
