//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestDashboardMeHandlerLoadsFromTenantTx(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "handler-dash-me"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "handler-dash-me@example.com", "Handler User"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), repo.RoleMember)
	now := time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC)
	sessionID := tdb.SeedSession(ctx, t, tenantID.String(), userID.String(), now.Add(-time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), sessionID, "v1", 0.64, now.Add(-50*time.Minute))

	rec := serveDashboardMe(t, tdb, tenantID, userID, now, "/v1/dashboard/me?days=7&limit=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", got)
	}
	var body contracts.DashboardMeResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User.ID != userID || body.User.DisplayName != "Handler User" {
		t.Fatalf("user mismatch: %+v", body.User)
	}
	if len(body.Trend) != 7 {
		t.Fatalf("trend len = %d, want 7", len(body.Trend))
	}
	if len(body.RecentSessions) != 1 || body.RecentSessions[0].ID.String() != sessionID {
		t.Fatalf("recent sessions mismatch: %+v", body.RecentSessions)
	}
}

func serveDashboardMe(
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	userID uuid.UUID,
	now time.Time,
	target string,
) *httptest.ResponseRecorder {
	t.Helper()
	h := dashboardMeHandler(discardLogger(), func() time.Time { return now })
	var rec *httptest.ResponseRecorder
	if err := db.WithTenant(context.Background(), tdb.AppPool, tenantID.String(), func(ctx context.Context, _ pgx.Tx) error {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(contracts.WithPrincipal(ctx, contracts.Principal{
			UserID:   userID,
			TenantID: tenantID,
			TokenID:  "test-token",
		}))
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return nil
	}); err != nil {
		t.Fatalf("serve dashboard me: %v", err)
	}
	return rec
}
