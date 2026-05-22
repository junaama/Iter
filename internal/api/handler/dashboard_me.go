package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	defaultDashboardMeDays  = 14
	maxDashboardMeDays      = 90
	defaultDashboardMeLimit = 10
	maxDashboardMeLimit     = 50

	dashboardDateLayout = "2006-01-02"
)

var errDashboardNoTx = errors.New("dashboard/me: tenant transaction missing")

type dashboardMeStore interface {
	LoadDashboardMe(ctx context.Context, userID uuid.UUID, days int, limit int, now time.Time) (repo.DashboardMe, error)
}

type liveDashboardMeStore struct{}

func (liveDashboardMeStore) LoadDashboardMe(
	ctx context.Context,
	userID uuid.UUID,
	days int,
	limit int,
	now time.Time,
) (repo.DashboardMe, error) {
	tx := db.FromContext(ctx)
	if tx == nil {
		return repo.DashboardMe{}, errDashboardNoTx
	}
	return repo.LoadDashboardMe(ctx, tx, userID, days, limit, now)
}

// DashboardMeHandler returns the HTTP handler mounted at GET /v1/dashboard/me.
func DashboardMeHandler(deps app.Deps) http.HandlerFunc {
	return dashboardMeHandler(deps.Logger, liveDashboardMeStore{}, time.Now)
}

func dashboardMeHandler(
	logger *slog.Logger,
	store dashboardMeStore,
	now func() time.Time,
) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil {
		store = liveDashboardMeStore{}
	}
	if now == nil {
		now = time.Now
	}

	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			writeDashboardJSON(w, http.StatusUnauthorized, dashboardError{Error: "unauthenticated"})
			return
		}

		days, err := boundedPositiveInt(r, "days", defaultDashboardMeDays, maxDashboardMeDays)
		if err != nil {
			writeDashboardJSON(w, http.StatusBadRequest, dashboardError{Error: "invalid_query"})
			return
		}
		limit, err := boundedPositiveInt(r, "limit", defaultDashboardMeLimit, maxDashboardMeLimit)
		if err != nil {
			writeDashboardJSON(w, http.StatusBadRequest, dashboardError{Error: "invalid_query"})
			return
		}

		me, err := store.LoadDashboardMe(r.Context(), principal.UserID, days, limit, now().UTC())
		if err != nil {
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				writeDashboardJSON(w, http.StatusNotFound, dashboardError{Error: "user_not_found"})
			case errors.Is(err, errDashboardNoTx):
				logger.ErrorContext(r.Context(), "dashboard_me_missing_tenant_tx")
				writeDashboardJSON(w, http.StatusInternalServerError, dashboardError{Error: "internal"})
			default:
				logger.ErrorContext(r.Context(), "dashboard_me_load_failed", "err", err)
				writeDashboardJSON(w, http.StatusInternalServerError, dashboardError{Error: "internal"})
			}
			return
		}

		writeDashboardJSON(w, http.StatusOK, dashboardMeResponse(me))
	}
}

type dashboardError struct {
	Error string `json:"error"`
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

func writeDashboardJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
