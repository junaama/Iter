//go:build integration

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

func TestStackShares_AddIdempotent(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userA := seedTenancy(ctx, t, tdb, "share-a")
	userB := uuid.MustParse(tdb.SeedUser(ctx, t, "share-b@example.com", "User-B"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userB.String(), repo.RoleMember)

	var stackID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.CreateStack(ctx, tx, newStack(tenantID, userA, "shared"))
		if err != nil {
			return err
		}
		stackID = s.ID
		return nil
	}); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	// Two AddShare calls — second is a no-op (idempotent).
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if err := repo.AddShare(ctx, tx, stackID, userB); err != nil {
			return err
		}
		if err := repo.AddShare(ctx, tx, stackID, userB); err != nil {
			return err
		}
		shares, err := repo.ListSharesForStack(ctx, tx, stackID)
		if err != nil {
			return err
		}
		if len(shares) != 1 {
			t.Fatalf("share rows = %d, want 1 (idempotent)", len(shares))
		}
		if shares[0].SharedWithUserID != userB {
			t.Fatalf("share target = %s, want %s", shares[0].SharedWithUserID, userB)
		}
		if shares[0].TenantID != tenantID {
			t.Fatalf("share tenant_id = %s, want %s (sourced from stack row)", shares[0].TenantID, tenantID)
		}
		return nil
	}); err != nil {
		t.Fatalf("AddShare: %v", err)
	}
}

func TestStackShares_AddValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "share-val")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if err := repo.AddShare(ctx, tx, uuid.Nil, uuid.New()); err == nil {
			t.Fatal("expected error on nil stack_id")
		}
		if err := repo.AddShare(ctx, tx, uuid.New(), uuid.Nil); err == nil {
			t.Fatal("expected error on nil shared_with_user_id")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestStackShares_ListSharedWithUser(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userA := seedTenancy(ctx, t, tdb, "share-listwith")
	userB := uuid.MustParse(tdb.SeedUser(ctx, t, "share-listwith-b@example.com", "User-B"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userB.String(), repo.RoleMember)

	var stackID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.CreateStack(ctx, tx, newStack(tenantID, userA, "A's stack"))
		if err != nil {
			return err
		}
		stackID = s.ID
		return repo.AddShare(ctx, tx, stackID, userB)
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Query as B — should see A's stack.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		shared, err := repo.ListSharedWithUser(ctx, tx, userB)
		if err != nil {
			return err
		}
		if len(shared) != 1 {
			t.Fatalf("len = %d, want 1", len(shared))
		}
		if shared[0].ID != stackID {
			t.Fatalf("stackID mismatch: %s vs %s", shared[0].ID, stackID)
		}
		if shared[0].UserID != userA {
			t.Fatalf("owner = %s, want %s", shared[0].UserID, userA)
		}

		// Query as A — A is the owner, not a target, so 0 shared-with.
		ownerShared, err := repo.ListSharedWithUser(ctx, tx, userA)
		if err != nil {
			return err
		}
		if len(ownerShared) != 0 {
			t.Fatalf("owner sees %d as-shared-with, want 0", len(ownerShared))
		}

		// ListSharesByUser.
		byB, err := repo.ListSharesByUser(ctx, tx, userB)
		if err != nil {
			return err
		}
		if len(byB) != 1 || byB[0].StackID != stackID {
			t.Fatalf("ListSharesByUser mismatch: %+v", byB)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestStackShares_Remove(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userA := seedTenancy(ctx, t, tdb, "share-rm")
	userB := uuid.MustParse(tdb.SeedUser(ctx, t, "share-rm-b@example.com", "User-B"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userB.String(), repo.RoleMember)

	var stackID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.CreateStack(ctx, tx, newStack(tenantID, userA, "x"))
		if err != nil {
			return err
		}
		stackID = s.ID
		return repo.AddShare(ctx, tx, stackID, userB)
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.RemoveShare(ctx, tx, stackID, userB)
	}); err != nil {
		t.Fatalf("RemoveShare: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		err := repo.RemoveShare(ctx, tx, stackID, userB)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on second remove, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestStackShares_CascadeOnStackDelete(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userA := seedTenancy(ctx, t, tdb, "share-cascade")
	userB := uuid.MustParse(tdb.SeedUser(ctx, t, "share-cascade-b@example.com", "User-B"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userB.String(), repo.RoleMember)

	var stackID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.CreateStack(ctx, tx, newStack(tenantID, userA, "x"))
		if err != nil {
			return err
		}
		stackID = s.ID
		return repo.AddShare(ctx, tx, stackID, userB)
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.DeleteStack(ctx, tx, stackID)
	}); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		shares, err := repo.ListSharesForStack(ctx, tx, stackID)
		if err != nil {
			return err
		}
		if len(shares) != 0 {
			t.Fatalf("cascade failed: %d share rows still present", len(shares))
		}
		return nil
	}); err != nil {
		t.Fatalf("verify cascade: %v", err)
	}
}
