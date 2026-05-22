//go:build integration

package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

// Tenants are not RLS-scoped (the table has no tenant_isolation policy
// — tenants IS the tenants table). The repo functions run against the
// iter_app pool through a "raw" begin/commit, but to keep the surface
// uniform with the RLS path we still call tx.Begin via WithBatch-style
// helpers in tests below. The simplest path: open a pgx.Tx directly
// against the superuser pool. We use that for setup-side tenant ops.
//
// For the integration tests of the tenant repo we use the superuser
// pgxpool because tenants don't require RLS — admin-only writes.

func TestInsertGetSoftDelete_Tenant(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	var inserted repo.Tenant
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.InsertTenant(ctx, tx, "Acme", repo.PlanTeam)
		if err != nil {
			return err
		}
		inserted = v
		return nil
	})
	if inserted.ID == uuid.Nil {
		t.Fatal("InsertTenant: empty id")
	}
	if inserted.Plan != repo.PlanTeam {
		t.Fatalf("InsertTenant: plan = %q want %q", inserted.Plan, repo.PlanTeam)
	}
	if inserted.Name != "Acme" {
		t.Fatalf("InsertTenant: name = %q", inserted.Name)
	}

	var fetched repo.Tenant
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.GetTenant(ctx, tx, inserted.ID)
		if err != nil {
			return err
		}
		fetched = v
		return nil
	})
	if fetched.ID != inserted.ID || fetched.Name != "Acme" {
		t.Fatalf("GetTenant mismatch: %+v", fetched)
	}
	if fetched.DeletedAt != nil {
		t.Fatalf("GetTenant: deleted_at should be nil, got %v", *fetched.DeletedAt)
	}

	// Soft-delete it.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		return repo.SoftDeleteTenant(ctx, tx, inserted.ID)
	})

	// Verify deleted_at populated.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.GetTenant(ctx, tx, inserted.ID)
		if err != nil {
			return err
		}
		if v.DeletedAt == nil {
			t.Fatalf("SoftDeleteTenant: deleted_at still nil")
		}
		return nil
	})

	// Idempotent second delete: rows affected = 0 → ErrNoRows.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		err := repo.SoftDeleteTenant(ctx, tx, inserted.ID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on second soft-delete, got %v", err)
		}
		return nil
	})
}

func TestInsertTenant_DefaultsPlan(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.InsertTenant(ctx, tx, "FreeCo", "")
		if err != nil {
			return err
		}
		if v.Plan != repo.PlanFree {
			t.Fatalf("default plan = %q, want free", v.Plan)
		}
		return nil
	})
}

func TestInsertTenant_RejectsEmptyName(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.InsertTenant(ctx, tx, "", repo.PlanFree)
		if err == nil {
			t.Fatal("expected error for empty name")
		}
		return nil
	})
}

func TestGetTenant_NotFound(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.GetTenant(ctx, tx, uuid.New())
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows, got %v", err)
		}
		return nil
	})
}

func TestListTenants_KeysetPaginationStable(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	// Seed 10 tenants. created_at defaults to now(); we want them
	// distinguishable so we sleep 1ms between inserts. timestamptz has
	// microsecond resolution so 1ms is more than enough.
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	ids := make([]uuid.UUID, 0, len(names))
	for _, name := range names {
		mustTx(t, ctx, pool, func(tx pgx.Tx) error {
			v, err := repo.InsertTenant(ctx, tx, name, repo.PlanFree)
			if err != nil {
				return err
			}
			ids = append(ids, v.ID)
			return nil
		})
		time.Sleep(2 * time.Millisecond)
	}

	// Page 1: 4 most recent.
	var page1 []repo.Tenant
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.ListTenants(ctx, tx, 4, time.Time{}, uuid.Nil)
		if err != nil {
			return err
		}
		page1 = v
		return nil
	})
	if len(page1) != 4 {
		t.Fatalf("page1 len = %d, want 4", len(page1))
	}

	// Insert a new tenant *between* pages — page 2 must NOT include it
	// because the cursor is anchored on page1's last (created_at, id).
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.InsertTenant(ctx, tx, "intruder", repo.PlanFree)
		return err
	})

	// Page 2: cursor from page1's last row.
	last := page1[len(page1)-1]
	var page2 []repo.Tenant
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.ListTenants(ctx, tx, 4, last.CreatedAt, last.ID)
		if err != nil {
			return err
		}
		page2 = v
		return nil
	})
	if len(page2) != 4 {
		t.Fatalf("page2 len = %d, want 4", len(page2))
	}
	// No overlap between pages.
	seen := map[uuid.UUID]struct{}{}
	for _, r := range page1 {
		seen[r.ID] = struct{}{}
	}
	for _, r := range page2 {
		if _, dup := seen[r.ID]; dup {
			t.Fatalf("page2 contains id from page1: %s", r.ID)
		}
		if r.Name == "intruder" {
			t.Fatalf("page2 contains intruder inserted after page1 was returned")
		}
	}
}
