package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

type fakeDashboardMeStore struct {
	resp repo.DashboardMe
	err  error

	calls int
	user  uuid.UUID
	days  int
	limit int
	now   time.Time
}

func (s *fakeDashboardMeStore) LoadDashboardMe(
	_ context.Context,
	userID uuid.UUID,
	days int,
	limit int,
	now time.Time,
) (repo.DashboardMe, error) {
	s.calls++
	s.user = userID
	s.days = days
	s.limit = limit
	s.now = now
	if s.err != nil {
		return repo.DashboardMe{}, s.err
	}
	return s.resp, nil
}

func TestDashboardMeHandler_DefaultsAndResponse(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	score := 0.71
	now := time.Date(2026, 5, 22, 15, 30, 0, 0, time.UTC)
	store := &fakeDashboardMeStore{
		resp: repo.DashboardMe{
			User: repo.DashboardUser{
				ID:          userID,
				DisplayName: "Priya Raman",
				Email:       "priya@example.com",
			},
			Trend: []repo.DashboardTrendPoint{
				{Day: now.AddDate(0, 0, -1).Truncate(24 * time.Hour)},
				{Day: now.Truncate(24 * time.Hour), CompositeScore: &score, SessionCount: 4},
			},
			RecentSessions: []repo.DashboardRecentSession{
				{
					ID:                    sessionID,
					StartedAt:             now.Add(-2 * time.Hour),
					CompositeScore:        &score,
					Harness:               "claude_code",
					RedactedPromptPreview: "first 120 chars",
				},
			},
		},
	}

	h := dashboardMeHandler(discardLogger(), store, func() time.Time { return now })
	req := dashboardMeRequest("/v1/dashboard/me", contracts.Principal{UserID: userID, TenantID: uuid.New()})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store calls: got %d want 1", store.calls)
	}
	if store.user != userID {
		t.Fatalf("user id: got %s want %s", store.user, userID)
	}
	if store.days != defaultDashboardMeDays || store.limit != defaultDashboardMeLimit {
		t.Fatalf("defaults: got days=%d limit=%d", store.days, store.limit)
	}
	if !store.now.Equal(now) {
		t.Fatalf("now: got %s want %s", store.now, now)
	}

	var body contracts.DashboardMeResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User.ID != userID || body.User.DisplayName != "Priya Raman" || body.User.Email != "priya@example.com" {
		t.Fatalf("user mismatch: %+v", body.User)
	}
	if len(body.Trend) != 2 || body.Trend[0].CompositeScore != nil || body.Trend[1].CompositeScore == nil {
		t.Fatalf("trend mismatch: %+v", body.Trend)
	}
	if body.Trend[1].Date != "2026-05-22" || *body.Trend[1].CompositeScore != score || body.Trend[1].SessionCount != 4 {
		t.Fatalf("trend point mismatch: %+v", body.Trend[1])
	}
	if len(body.RecentSessions) != 1 || body.RecentSessions[0].ID != sessionID {
		t.Fatalf("recent mismatch: %+v", body.RecentSessions)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control: got %q want no-store", got)
	}
}

func TestDashboardMeHandler_QueryCaps(t *testing.T) {
	userID := uuid.New()
	store := &fakeDashboardMeStore{
		resp: repo.DashboardMe{
			User: repo.DashboardUser{ID: userID, DisplayName: "Priya", Email: "priya@example.com"},
		},
	}

	h := dashboardMeHandler(discardLogger(), store, func() time.Time { return time.Now() })
	req := dashboardMeRequest("/v1/dashboard/me?days=500&limit=500", contracts.Principal{UserID: userID, TenantID: uuid.New()})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if store.days != maxDashboardMeDays || store.limit != maxDashboardMeLimit {
		t.Fatalf("caps: got days=%d limit=%d", store.days, store.limit)
	}
}

func TestDashboardMeHandler_InvalidQuery(t *testing.T) {
	tests := []string{
		"/v1/dashboard/me?days=abc",
		"/v1/dashboard/me?days=0",
		"/v1/dashboard/me?limit=-1",
	}

	for _, path := range tests {
		path := path
		t.Run(path, func(t *testing.T) {
			store := &fakeDashboardMeStore{}
			h := dashboardMeHandler(discardLogger(), store, time.Now)
			req := dashboardMeRequest(path, contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
			rec := httptest.NewRecorder()
			h(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d want 400", rec.Code)
			}
			if store.calls != 0 {
				t.Fatalf("store should not be called on invalid query; got %d", store.calls)
			}
			assertDashboardError(t, rec.Body.Bytes(), "invalid_query")
		})
	}
}

func TestDashboardMeHandler_RequiresPrincipal(t *testing.T) {
	store := &fakeDashboardMeStore{}
	h := dashboardMeHandler(discardLogger(), store, time.Now)

	req := httptest.NewRequest(http.MethodGet, "/v1/dashboard/me", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
	if store.calls != 0 {
		t.Fatalf("store should not be called without principal; got %d", store.calls)
	}
	assertDashboardError(t, rec.Body.Bytes(), "unauthenticated")
}

func TestDashboardMeHandler_ErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{"user missing", pgx.ErrNoRows, http.StatusNotFound, "user_not_found"},
		{"missing tenant tx", errDashboardNoTx, http.StatusInternalServerError, "internal"},
		{"store failure", errors.New("boom"), http.StatusInternalServerError, "internal"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeDashboardMeStore{err: tc.err}
			h := dashboardMeHandler(discardLogger(), store, time.Now)
			req := dashboardMeRequest("/v1/dashboard/me", contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
			rec := httptest.NewRecorder()
			h(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status: got %d want %d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			assertDashboardError(t, rec.Body.Bytes(), tc.wantError)
		})
	}
}

func dashboardMeRequest(path string, principal contracts.Principal) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req.WithContext(contracts.WithPrincipal(req.Context(), principal))
}

func assertDashboardError(t *testing.T, body []byte, want string) {
	t.Helper()
	var got dashboardError
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if got.Error != want {
		t.Fatalf("error body: got %q want %q", got.Error, want)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
