//go:build integration

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

func TestTenantUsers_Lifecycle(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	// Seed tenant + two users via superuser (no RLS on these tables).
	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "Acme"))
	userA := uuid.MustParse(tdb.SeedUser(ctx, t, "a@example.com", "A"))
	userB := uuid.MustParse(tdb.SeedUser(ctx, t, "b@example.com", "B"))

	// Insert two memberships with different roles.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := repo.InsertTenantUser(ctx, tx, tenantID, userA, repo.RoleOwner); err != nil {
			return err
		}
		if _, err := repo.InsertTenantUser(ctx, tx, tenantID, userB, repo.RoleMember); err != nil {
			return err
		}
		return nil
	})

	// Get the owner row back.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		got, err := repo.GetTenantUser(ctx, tx, tenantID, userA)
		if err != nil {
			return err
		}
		if got.Role != repo.RoleOwner {
			t.Fatalf("Get role = %q, want owner", got.Role)
		}
		return nil
	})

	// List by tenant returns both, ordered by joined_at ASC.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		members, err := repo.ListTenantUsersByTenant(ctx, tx, tenantID)
		if err != nil {
			return err
		}
		if len(members) != 2 {
			t.Fatalf("ListByTenant len = %d, want 2", len(members))
		}
		return nil
	})

	// List by user — userA is only in tenantID.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		mems, err := repo.ListTenantUsersByUser(ctx, tx, userA)
		if err != nil {
			return err
		}
		if len(mems) != 1 || mems[0].TenantID != tenantID {
			t.Fatalf("ListByUser unexpected: %+v", mems)
		}
		return nil
	})

	// Update role: B → admin.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		return repo.UpdateTenantUserRole(ctx, tx, tenantID, userB, repo.RoleAdmin)
	})
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		got, err := repo.GetTenantUser(ctx, tx, tenantID, userB)
		if err != nil {
			return err
		}
		if got.Role != repo.RoleAdmin {
			t.Fatalf("updated role = %q", got.Role)
		}
		return nil
	})

	// Delete B's membership.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		return repo.DeleteTenantUser(ctx, tx, tenantID, userB)
	})
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.GetTenantUser(ctx, tx, tenantID, userB)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows after delete, got %v", err)
		}
		err = repo.DeleteTenantUser(ctx, tx, tenantID, userB)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on second delete, got %v", err)
		}
		return nil
	})
}

func TestTenantUsers_RejectsInvalidRole(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "T"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "x@y.z", "X"))

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.InsertTenantUser(ctx, tx, tenantID, userID, "manager")
		if err == nil {
			t.Fatal("expected error for invalid role")
		}
		return nil
	})

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := repo.InsertTenantUser(ctx, tx, tenantID, userID, repo.RoleOwner); err != nil {
			return err
		}
		err := repo.UpdateTenantUserRole(ctx, tx, tenantID, userID, "ceo")
		if err == nil {
			t.Fatal("expected error for invalid role on update")
		}
		return nil
	})
}

func TestTenantUsers_CascadeOnTenantDelete(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "Doomed"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "z@example.com", "Z"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), repo.RoleMember)

	// Hard-delete the tenant via superuser; tenant_users must cascade.
	if _, err := tdb.Super.ExecContext(ctx,
		"DELETE FROM tenants WHERE id = $1", tenantID,
	); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.GetTenantUser(ctx, tx, tenantID, userID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected cascade — ErrNoRows — got %v", err)
		}
		return nil
	})
}
