package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/iter-dev/iter/internal/api/respond"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	defaultMemberLimit  = 50
	defaultPatternLimit = 10
	maxTeamLimit        = 100
)

// DashboardTeamHandler serves GET /v1/dashboard/team.
func DashboardTeamHandler(deps app.Deps) http.HandlerFunc {
	return dashboardTeamHandlerWith(time.Now, deps.Logger)
}

func dashboardTeamHandlerWith(
	now func() time.Time,
	logger *slog.Logger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}

		memberLimit, ok := parseTeamLimit(r, "member_limit", defaultMemberLimit)
		if !ok {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_member_limit"})
			return
		}
		patternLimit, ok := parseTeamLimit(r, "pattern_limit", defaultPatternLimit)
		if !ok {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_pattern_limit"})
			return
		}

		tx, err := db.RequireTx(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_team_missing_tenant_tx")
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}

		since := now().UTC().AddDate(0, 0, -30)
		members, err := repo.ListTeamMemberAggregates(r.Context(), tx, since, memberLimit)
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_team_members_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}
		patterns, err := repo.ListTeamTopPatterns(r.Context(), tx, since, patternLimit)
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_team_patterns_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}
		invite, err := repo.GetTeamInviteSettings(r.Context(), tx, principal.TenantID)
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_team_invite_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}

		resp := contracts.DashboardTeamResponse{
			Members:     mapTeamMembers(members),
			TopPatterns: mapTeamPatterns(patterns),
		}
		if invite.Enabled {
			resp.Invite = &contracts.TeamInvite{
				Enabled:            true,
				InviteLinkTemplate: invite.InviteLinkTemplate,
			}
		}
		respond.JSON(w, http.StatusOK, resp)
	}
}

func parseTeamLimit(r *http.Request, key string, fallback int) (int, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n > maxTeamLimit {
		return maxTeamLimit, true
	}
	return n, true
}

func mapTeamMembers(in []repo.TeamMemberAggregate) []contracts.TeamMemberAggregate {
	out := make([]contracts.TeamMemberAggregate, 0, len(in))
	for _, m := range in {
		out = append(out, contracts.TeamMemberAggregate{
			UserID:                m.UserID,
			DisplayName:           m.DisplayName,
			SessionCount30d:       m.SessionCount30d,
			MeanCompositeScore30d: m.MeanCompositeScore30d,
		})
	}
	return out
}

func mapTeamPatterns(in []repo.TeamPatternAggregate) []contracts.TeamPatternAggregate {
	out := make([]contracts.TeamPatternAggregate, 0, len(in))
	for _, p := range in {
		out = append(out, contracts.TeamPatternAggregate{
			PatternID:   p.PatternID,
			Preview:     p.Preview,
			UsesCount:   p.UsesCount,
			TenantsUsed: p.TenantsUsed,
			AvgScore:    p.AvgScore,
		})
	}
	return out
}
