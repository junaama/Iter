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

	"github.com/iter-dev/iter/internal/api/authz"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestListSessionsStaleJWTRoleDoesNotAuthorizeUserFilter(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "handler-sessions-stale-role"))
	adminID := uuid.MustParse(tdb.SeedUser(ctx, t, "sessions-admin@example.com", "Admin User"))
	memberID := uuid.MustParse(tdb.SeedUser(ctx, t, "sessions-member@example.com", "Member User"))
	tdb.SeedMembership(ctx, t, tenantID.String(), adminID.String(), repo.RoleAdmin)
	tdb.SeedMembership(ctx, t, tenantID.String(), memberID.String(), repo.RoleMember)

	now := time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC)
	adminSession := seedListSession(ctx, t, tdb, tenantID, adminID, now, "admin prompt", repo.ClassificationClean)
	memberSession := seedListSession(ctx, t, tdb, tenantID, memberID, now.Add(time.Minute), "member prompt", repo.ClassificationClean)

	rec := serveListSessions(t, tdb, contracts.Principal{
		UserID:   memberID,
		TenantID: tenantID,
		Roles:    []string{repo.RoleAdmin},
		TokenID:  "stale-admin-role",
	}, "/v1/sessions?user_id="+adminID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	var body contracts.ListSessionsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sessions) != 1 || body.Sessions[0].ID != memberSession {
		t.Fatalf("stale JWT role should be scoped to member session %s, got %+v", memberSession, body.Sessions)
	}
	if body.Sessions[0].ID == adminSession {
		t.Fatalf("stale JWT role exposed admin-filtered session %s", adminSession)
	}
}

func TestListSessionsDBAdminCanFilterDirtySessions(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "handler-sessions-admin-filter"))
	adminID := uuid.MustParse(tdb.SeedUser(ctx, t, "dirty-admin@example.com", "Admin User"))
	memberID := uuid.MustParse(tdb.SeedUser(ctx, t, "dirty-member@example.com", "Member User"))
	tdb.SeedMembership(ctx, t, tenantID.String(), adminID.String(), repo.RoleAdmin)
	tdb.SeedMembership(ctx, t, tenantID.String(), memberID.String(), repo.RoleMember)

	now := time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC)
	dirtySession := seedListSession(ctx, t, tdb, tenantID, memberID, now, "dirty prompt", repo.ClassificationDirty)
	seedListSession(ctx, t, tdb, tenantID, adminID, now.Add(time.Minute), "admin prompt", repo.ClassificationClean)

	rec := serveListSessions(t, tdb, contracts.Principal{
		UserID:   adminID,
		TenantID: tenantID,
		TokenID:  "db-admin-no-roles-claim",
	}, "/v1/sessions?user_id="+memberID.String()+"&classification=dirty")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	var body contracts.ListSessionsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sessions) != 1 || body.Sessions[0].ID != dirtySession {
		t.Fatalf("admin dirty filter mismatch: want %s got %+v", dirtySession, body.Sessions)
	}
}

func serveListSessions(
	t *testing.T,
	tdb *dbtest.TestDB,
	principal contracts.Principal,
	target string,
) *httptest.ResponseRecorder {
	t.Helper()
	h := listSessionsHandler(discardLogger())
	var rec *httptest.ResponseRecorder
	if err := db.WithTenant(context.Background(), tdb.AppPool, principal.TenantID.String(), func(ctx context.Context, _ pgx.Tx) error {
		ctx = authz.WithAdminCache(ctx)
		ctx = contracts.WithPrincipal(ctx, principal)
		req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return nil
	}); err != nil {
		t.Fatalf("serve list sessions: %v", err)
	}
	return rec
}

func seedListSession(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	userID uuid.UUID,
	startedAt time.Time,
	redactedPrompt string,
	classification string,
) uuid.UUID {
	t.Helper()
	var id string
	if err := tdb.Super.QueryRowContext(ctx, `
		INSERT INTO sessions (
		  tenant_id, user_id, harness, model, tools,
		  started_at, ended_at, redacted_prompt, classification
		) VALUES ($1, $2, 'codex', 'gpt-5', ARRAY[]::text[], $3, $4, $5, $6)
		RETURNING id
	`, tenantID.String(), userID.String(), startedAt, nil, redactedPrompt, classification).Scan(&id); err != nil {
		t.Fatalf("seed list session: %v", err)
	}
	return uuid.MustParse(id)
}
