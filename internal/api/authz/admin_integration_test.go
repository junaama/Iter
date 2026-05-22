//go:build integration

package authz

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestIsAdminCachesMembershipPerContext(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "authz-cache"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "authz-cache@example.com", "Authz User"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), repo.RoleAdmin)
	principal := contracts.Principal{UserID: userID, TenantID: tenantID, TokenID: "authz-cache"}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(txCtx context.Context, _ pgx.Tx) error {
		reqCtx := contracts.WithPrincipal(WithAdminCache(txCtx), principal)
		admin, err := IsAdmin(reqCtx)
		if err != nil {
			t.Fatalf("initial IsAdmin: %v", err)
		}
		if !admin {
			t.Fatal("initial IsAdmin = false, want true")
		}

		if _, err := tdb.Super.ExecContext(ctx, `
			UPDATE tenant_users SET role = $3
			 WHERE tenant_id = $1 AND user_id = $2
		`, tenantID.String(), userID.String(), repo.RoleMember); err != nil {
			t.Fatalf("demote membership: %v", err)
		}

		cached, err := IsAdmin(reqCtx)
		if err != nil {
			t.Fatalf("cached IsAdmin: %v", err)
		}
		if !cached {
			t.Fatal("cached IsAdmin = false, want true for same request context")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant cached request: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(txCtx context.Context, _ pgx.Tx) error {
		reqCtx := contracts.WithPrincipal(WithAdminCache(txCtx), principal)
		admin, err := IsAdmin(reqCtx)
		if err != nil {
			t.Fatalf("fresh IsAdmin: %v", err)
		}
		if admin {
			t.Fatal("fresh IsAdmin = true after demotion, want false")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant fresh request: %v", err)
	}
}
