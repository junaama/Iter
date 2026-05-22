//go:build integration

package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"github.com/iter-dev/iter/internal/api/authz"
	"github.com/iter-dev/iter/internal/api/handler"
	"github.com/iter-dev/iter/internal/api/middleware"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestDashboardTeam_AdminGetsAggregates(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "team-api-admin"))
	adminID := uuid.MustParse(tdb.SeedUser(ctx, t, "admin-api@example.com", "Admin User"))
	formerID := uuid.MustParse(tdb.SeedUser(ctx, t, "former-api@example.com", "Hidden Name"))
	tdb.SeedMembership(ctx, t, tenantID.String(), adminID.String(), repo.RoleAdmin)

	sessionID := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), adminID.String(), now.Add(-time.Hour)))
	formerSessionID := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), formerID.String(), now.Add(-2*time.Hour)))
	tdb.SeedScore(ctx, t, tenantID.String(), sessionID.String(), "v1", 0.64, now.Add(-50*time.Minute))
	tdb.SeedScore(ctx, t, tenantID.String(), formerSessionID.String(), "v1", 0.31, now.Add(-90*time.Minute))
	if _, err := tdb.Super.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, formerID); err != nil {
		t.Fatalf("soft-delete former user: %v", err)
	}
	seedPattern(t, ctx, tdb, tenantID, "Use the internal testing harness", 12, 6, now.Add(-30*time.Minute))

	rec := serveDashboardTeam(t, tdb, contracts.Principal{
		UserID:   adminID,
		TenantID: tenantID,
		TokenID:  "jti-admin",
	}, "/v1/dashboard/team")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}

	var body contracts.DashboardTeamResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Members) != 2 {
		t.Fatalf("members len = %d, want 2: %#v", len(body.Members), body.Members)
	}
	foundFormer := false
	for _, member := range body.Members {
		if member.UserID == formerID {
			foundFormer = true
			if member.DisplayName != "former member" {
				t.Fatalf("former display name = %q", member.DisplayName)
			}
		}
	}
	if !foundFormer {
		t.Fatalf("former member missing from response: %#v", body.Members)
	}
	if len(body.TopPatterns) != 1 {
		t.Fatalf("top patterns len = %d, want 1", len(body.TopPatterns))
	}
	if body.TopPatterns[0].UsesCount != 12 || body.TopPatterns[0].TenantsUsed != 1 {
		t.Fatalf("top pattern counters = %#v", body.TopPatterns[0])
	}
	if body.Invite == nil || !body.Invite.Enabled || !strings.Contains(body.Invite.InviteLinkTemplate, tenantID.String()) {
		t.Fatalf("invite block missing/invalid: %#v", body.Invite)
	}
}

func TestDashboardTeam_MemberForbidden(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "team-api-member"))
	memberID := uuid.MustParse(tdb.SeedUser(ctx, t, "member-api@example.com", "Member User"))
	tdb.SeedMembership(ctx, t, tenantID.String(), memberID.String(), repo.RoleMember)

	rec := serveDashboardTeam(t, tdb, contracts.Principal{
		UserID:   memberID,
		TenantID: tenantID,
		TokenID:  "jti-member",
	}, "/v1/dashboard/team")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"forbidden","required_role":"admin"}` {
		t.Fatalf("forbidden body = %q", rec.Body.String())
	}
}

func TestDashboardTeam_EmptyTenantReturnsEmptyArrays(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "team-api-empty"))
	adminID := uuid.MustParse(tdb.SeedUser(ctx, t, "empty-admin@example.com", "Empty Admin"))
	tdb.SeedMembership(ctx, t, tenantID.String(), adminID.String(), repo.RoleOwner)

	rec := serveDashboardTeam(t, tdb, contracts.Principal{
		UserID:   adminID,
		TenantID: tenantID,
		TokenID:  "jti-empty",
	}, "/v1/dashboard/team")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}

	var body contracts.DashboardTeamResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Members) != 0 || len(body.TopPatterns) != 0 {
		t.Fatalf("want empty arrays, got members=%d patterns=%d", len(body.Members), len(body.TopPatterns))
	}
}

func serveDashboardTeam(
	t *testing.T,
	tdb *dbtest.TestDB,
	principal contracts.Principal,
	path string,
) *httptest.ResponseRecorder {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := handler.DashboardTeamHandler(app.Deps{Logger: logger})
	chain := middleware.Tenant(tdb.AppPool, middleware.WithTenantLogger(logger))(
		authz.AdminCache(requireAdmin(logger)(h)),
	)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(contracts.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	return rec
}

func seedPattern(
	t *testing.T,
	ctx context.Context,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	prompt string,
	hitCount, acceptCount int,
	lastUsedAt time.Time,
) {
	t.Helper()
	vec := make([]float32, 1536)
	vec[0] = 1
	var suggestion repo.Suggestion
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		suggestion, err = repo.UpsertSuggestion(ctx, tx, repo.Suggestion{
			TenantID:           tenantID,
			SourcePrompt:       prompt,
			SourceEmbedding:    pgvector.NewVector(vec),
			RefinedPrompt:      prompt + " refined",
			EvidenceSessionIDs: []uuid.UUID{},
		})
		return err
	}); err != nil {
		t.Fatalf("upsert suggestion: %v", err)
	}
	if _, err := tdb.Super.ExecContext(ctx, `
		UPDATE suggestions
		   SET hit_count = $2,
		       accept_count = $3,
		       last_used_at = $4
		 WHERE id = $1
	`, suggestion.ID, hitCount, acceptCount, lastUsedAt); err != nil {
		t.Fatalf("update suggestion counters: %v", err)
	}
}
