package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/api/respond"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestDashboardMeResponseMapsRepoProjection(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	score := 0.71
	now := time.Date(2026, 5, 22, 15, 30, 0, 0, time.UTC)

	body := dashboardMeResponse(repo.DashboardMe{
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
	})

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
}

func TestDashboardMeHandlerInvalidQueryDoesNotNeedTx(t *testing.T) {
	h := dashboardMeHandler(discardLogger(), time.Now)
	req := dashboardMeRequest("/v1/dashboard/me?days=abc", contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 body=%s", rec.Code, rec.Body.String())
	}
	assertAPIError(t, rec.Body.Bytes(), "invalid_query")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control: got %q want no-store", got)
	}
}

func TestDashboardMeHandlerRequiresPrincipal(t *testing.T) {
	h := dashboardMeHandler(discardLogger(), time.Now)
	req := httptest.NewRequest(http.MethodGet, "/v1/dashboard/me", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
	assertAPIError(t, rec.Body.Bytes(), "unauthenticated")
}

func TestDashboardMeHandlerMissingTx(t *testing.T) {
	h := dashboardMeHandler(discardLogger(), time.Now)
	req := dashboardMeRequest("/v1/dashboard/me", contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500 body=%s", rec.Code, rec.Body.String())
	}
	assertAPIError(t, rec.Body.Bytes(), "internal")
}

func dashboardMeRequest(path string, principal contracts.Principal) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req.WithContext(contracts.WithPrincipal(req.Context(), principal))
}

func assertAPIError(t *testing.T, body []byte, want string) {
	t.Helper()
	var got respond.Error
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
