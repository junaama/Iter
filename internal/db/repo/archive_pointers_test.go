//go:build integration

package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

func TestArchivePointers_InsertGet(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "arch-rt")

	// We need a real session to point at (so RLS-tenancy is realistic).
	var sessionID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		sessionID = s.ID
		return nil
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	uri := "r2://iter-archive/sessions/" + sessionID.String() + ".json.gz"

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.InsertPointer(ctx, tx, sessionID, tenantID, uri)
	}); err != nil {
		t.Fatalf("InsertPointer: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.GetForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if got.ObjectURI != uri {
			t.Fatalf("ObjectURI = %q, want %q", got.ObjectURI, uri)
		}
		if got.TenantID != tenantID {
			t.Fatalf("TenantID = %s, want %s", got.TenantID, tenantID)
		}
		if got.ArchivedAt.IsZero() {
			t.Fatal("ArchivedAt zero")
		}
		return nil
	}); err != nil {
		t.Fatalf("GetForSession: %v", err)
	}
}

func TestArchivePointers_InsertValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "arch-val")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if err := repo.InsertPointer(ctx, tx, uuid.Nil, tenantID, "x"); err == nil {
			t.Fatal("expected error on nil session_id")
		}
		if err := repo.InsertPointer(ctx, tx, uuid.New(), uuid.Nil, "x"); err == nil {
			t.Fatal("expected error on nil tenant_id")
		}
		if err := repo.InsertPointer(ctx, tx, uuid.New(), tenantID, ""); err == nil {
			t.Fatal("expected error on empty uri")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestArchivePointers_RLSScope_CrossTenantInvisible(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantA, userA := seedTenancy(ctx, t, tdb, "arch-a")
	tenantB, _ := seedTenancy(ctx, t, tdb, "arch-b")

	var sessionA uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantA, userA, "claude_code", "m"))
		if err != nil {
			return err
		}
		sessionA = s.ID
		return repo.InsertPointer(ctx, tx, sessionA, tenantA, "r2://a/"+sessionA.String())
	}); err != nil {
		t.Fatalf("seed A: %v", err)
	}

	// Under B's tenant, A's pointer must be invisible.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.GetForSession(ctx, tx, sessionA)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("RLS leak: B saw A's pointer: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant B: %v", err)
	}
}

func TestArchivePointers_ListBeforeDate(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "arch-list")

	// Seed 3 pointers across different times via direct superuser write
	// (archived_at default is now()).
	var sessionIDs [3]uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i := range sessionIDs {
			s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
			if err != nil {
				return err
			}
			sessionIDs[i] = s.ID
			if err := repo.InsertPointer(ctx, tx, sessionIDs[i], tenantID, "r2://b/"+s.ID.String()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Far-future cutoff returns all 3, oldest-first.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		list, err := repo.ListBeforeDate(ctx, tx, time.Now().Add(time.Hour), 0)
		if err != nil {
			return err
		}
		if len(list) != 3 {
			t.Fatalf("len = %d, want 3", len(list))
		}
		// Verify ascending order.
		for i := 1; i < len(list); i++ {
			if list[i].ArchivedAt.Before(list[i-1].ArchivedAt) {
				t.Fatalf("list not ordered ASC at i=%d", i)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("ListBeforeDate: %v", err)
	}

	// Far-past cutoff returns 0.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		list, err := repo.ListBeforeDate(ctx, tx, time.Now().Add(-24*time.Hour), 10)
		if err != nil {
			return err
		}
		if len(list) != 0 {
			t.Fatalf("len = %d, want 0", len(list))
		}
		return nil
	}); err != nil {
		t.Fatalf("ListBeforeDate past: %v", err)
	}

	// Explicit limit honored.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		list, err := repo.ListBeforeDate(ctx, tx, time.Now().Add(time.Hour), 2)
		if err != nil {
			return err
		}
		if len(list) != 2 {
			t.Fatalf("limit=2 len = %d", len(list))
		}
		return nil
	}); err != nil {
		t.Fatalf("limit: %v", err)
	}
}
