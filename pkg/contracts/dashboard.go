package contracts

import (
	"time"

	"github.com/google/uuid"
)

// DashboardUser is the caller identity projected into dashboard responses.
type DashboardUser struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
}

// DashboardTrendPoint is one UTC date bucket in the Me dashboard trend.
//
// CompositeScore is nil when no scored sessions contributed to the bucket.
type DashboardTrendPoint struct {
	Date           string   `json:"date"`
	CompositeScore *float64 `json:"composite_score"`
	SessionCount   int      `json:"session_count"`
}

// DashboardRecentSession is the compact session row shown on Dashboard / Me.
//
// CompositeScore is nil when the session has not been scored yet.
type DashboardRecentSession struct {
	ID                    uuid.UUID `json:"id"`
	StartedAt             time.Time `json:"started_at"`
	CompositeScore        *float64  `json:"composite_score"`
	Harness               string    `json:"harness"`
	RedactedPromptPreview string    `json:"redacted_prompt_preview"`
}

// DashboardMeResponse mirrors contracts.py DashboardMeResponse and is returned
// by GET /v1/dashboard/me.
type DashboardMeResponse struct {
	User           DashboardUser            `json:"user"`
	Trend          []DashboardTrendPoint    `json:"trend"`
	RecentSessions []DashboardRecentSession `json:"recent_sessions"`
}
