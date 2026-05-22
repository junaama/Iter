package contracts

import "github.com/google/uuid"

// DashboardTeamResponse mirrors contracts.py DashboardTeamResponse for
// GET /v1/dashboard/team.
type DashboardTeamResponse struct {
	Members     []TeamMemberAggregate  `json:"members"`
	TopPatterns []TeamPatternAggregate `json:"top_patterns"`
	Invite      *TeamInvite            `json:"invite,omitempty"`
}

// TeamMemberAggregate is one 30-day member row on the team dashboard.
type TeamMemberAggregate struct {
	UserID                uuid.UUID `json:"user_id"`
	DisplayName           string    `json:"display_name"`
	SessionCount30d       int       `json:"session_count_30d"`
	MeanCompositeScore30d *float64  `json:"mean_composite_score_30d"`
}

// TeamPatternAggregate is one prompt-pattern row on the team dashboard.
type TeamPatternAggregate struct {
	PatternID   uuid.UUID `json:"pattern_id"`
	Preview     string    `json:"preview"`
	UsesCount   int       `json:"uses_count"`
	TenantsUsed int       `json:"tenants_used"`
	AvgScore    float64   `json:"avg_score"`
}

// TeamInvite is present only when tenant settings enable team invites.
type TeamInvite struct {
	Enabled            bool   `json:"enabled"`
	InviteLinkTemplate string `json:"invite_link_template"`
}
