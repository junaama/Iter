package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

type dashboardTeamStore interface {
	ListMembers(context.Context, pgx.Tx, time.Time, int) ([]repo.TeamMemberAggregate, error)
	ListPatterns(context.Context, pgx.Tx, time.Time, int) ([]repo.TeamPatternAggregate, error)
	GetInviteSettings(context.Context, pgx.Tx, uuid.UUID) (repo.TeamInviteSettings, error)
}

type repoDashboardTeamStore struct{}

func (repoDashboardTeamStore) ListMembers(
	ctx context.Context,
	tx pgx.Tx,
	since time.Time,
	limit int,
) ([]repo.TeamMemberAggregate, error) {
	return repo.ListTeamMemberAggregates(ctx, tx, since, limit)
}

func (repoDashboardTeamStore) ListPatterns(
	ctx context.Context,
	tx pgx.Tx,
	since time.Time,
	limit int,
) ([]repo.TeamPatternAggregate, error) {
	return repo.ListTeamTopPatterns(ctx, tx, since, limit)
}

func (repoDashboardTeamStore) GetInviteSettings(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
) (repo.TeamInviteSettings, error) {
	return repo.GetTeamInviteSettings(ctx, tx, tenantID)
}

// DashboardTeamHandler serves GET /v1/dashboard/team.
func DashboardTeamHandler(deps app.Deps) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return dashboardTeamHandlerWith(repoDashboardTeamStore{}, time.Now, logger)
}

func dashboardTeamHandlerWith(
	store dashboardTeamStore,
	now func() time.Time,
	logger *slog.Logger,
) http.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			writeDashboardTeamJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}

		memberLimit, ok := parseTeamLimit(r, "member_limit", defaultMemberLimit)
		if !ok {
			writeDashboardTeamJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_member_limit"})
			return
		}
		patternLimit, ok := parseTeamLimit(r, "pattern_limit", defaultPatternLimit)
		if !ok {
			writeDashboardTeamJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_pattern_limit"})
			return
		}

		tx := db.FromContext(r.Context())
		if tx == nil {
			logger.ErrorContext(r.Context(), "dashboard_team_missing_tenant_tx")
			writeDashboardTeamJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}

		since := now().UTC().AddDate(0, 0, -30)
		members, err := store.ListMembers(r.Context(), tx, since, memberLimit)
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_team_members_failed", "err", err)
			writeDashboardTeamJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		patterns, err := store.ListPatterns(r.Context(), tx, since, patternLimit)
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_team_patterns_failed", "err", err)
			writeDashboardTeamJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		invite, err := store.GetInviteSettings(r.Context(), tx, principal.TenantID)
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_team_invite_failed", "err", err)
			writeDashboardTeamJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
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
		writeDashboardTeamJSON(w, http.StatusOK, resp)
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

func writeDashboardTeamJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
