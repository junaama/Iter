package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/api/respond"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	defaultDashboardMeDays  = 14
	maxDashboardMeDays      = 90
	defaultDashboardMeLimit = 10
	maxDashboardMeLimit     = 50

	dashboardDateLayout = "2006-01-02"
)

// DashboardMeHandler returns the HTTP handler mounted at GET /v1/dashboard/me.
func DashboardMeHandler(deps app.Deps) http.HandlerFunc {
	return dashboardMeHandler(deps.Logger, time.Now)
}

func dashboardMeHandler(
	logger *slog.Logger,
	now func() time.Time,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}

		days, err := boundedPositiveInt(r, "days", defaultDashboardMeDays, maxDashboardMeDays)
		if err != nil {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_query"})
			return
		}
		limit, err := boundedPositiveInt(r, "limit", defaultDashboardMeLimit, maxDashboardMeLimit)
		if err != nil {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_query"})
			return
		}

		tx, err := db.RequireTx(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "dashboard_me_missing_tenant_tx")
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
			return
		}

		me, err := repo.LoadDashboardMe(r.Context(), tx, principal.UserID, days, limit, now().UTC())
		if err != nil {
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				respond.JSON(w, http.StatusNotFound, respond.Error{Error: "user_not_found"})
			default:
				logger.ErrorContext(r.Context(), "dashboard_me_load_failed", "err", err)
				respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
			}
			return
		}

		respond.JSON(w, http.StatusOK, dashboardMeResponse(me))
	}
}

func boundedPositiveInt(r *http.Request, key string, def int, max int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	if value > max {
		return max, nil
	}
	return value, nil
}

func dashboardMeResponse(me repo.DashboardMe) contracts.DashboardMeResponse {
	trend := make([]contracts.DashboardTrendPoint, 0, len(me.Trend))
	for _, p := range me.Trend {
		trend = append(trend, contracts.DashboardTrendPoint{
			Date:           p.Day.UTC().Format(dashboardDateLayout),
			CompositeScore: p.CompositeScore,
			SessionCount:   p.SessionCount,
		})
	}

	recent := make([]contracts.DashboardRecentSession, 0, len(me.RecentSessions))
	for _, s := range me.RecentSessions {
		recent = append(recent, contracts.DashboardRecentSession{
			ID:                    s.ID,
			StartedAt:             s.StartedAt.UTC(),
			CompositeScore:        s.CompositeScore,
			Harness:               s.Harness,
			RedactedPromptPreview: s.RedactedPromptPreview,
		})
	}

	return contracts.DashboardMeResponse{
		User: contracts.DashboardUser{
			ID:          me.User.ID,
			DisplayName: me.User.DisplayName,
			Email:       me.User.Email,
		},
		Trend:          trend,
		RecentSessions: recent,
	}
}
