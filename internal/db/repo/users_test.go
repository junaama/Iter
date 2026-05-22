//go:build integration

package repo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

func TestInsertGetUser(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	var u repo.User
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.InsertUser(ctx, tx, "alice@example.com", "Alice")
		if err != nil {
			return err
		}
		u = v
		return nil
	})

	if u.ID == uuid.Nil {
		t.Fatal("InsertUser: empty id")
	}

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		got, err := repo.GetUser(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if got.Email != "alice@example.com" || got.DisplayName != "Alice" {
			t.Fatalf("GetUser mismatch: %+v", got)
		}
		return nil
	})
}

func TestGetUserByEmail_CaseInsensitive(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.InsertUser(ctx, tx, "Bob@Example.com", "Bob")
		return err
	})

	// citext: lookup by lowercase variant returns the same row.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		got, err := repo.GetUserByEmail(ctx, tx, "bob@example.com")
		if err != nil {
			return err
		}
		if !strings.EqualFold(got.Email, "Bob@Example.com") {
			t.Fatalf("citext lookup mismatch: got %q", got.Email)
		}
		return nil
	})
}

func TestInsertUser_RejectsEmptyFields(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := repo.InsertUser(ctx, tx, "", "Alice"); err == nil {
			t.Fatal("expected error for empty email")
		}
		if _, err := repo.InsertUser(ctx, tx, "x@y.z", ""); err == nil {
			t.Fatal("expected error for empty display_name")
		}
		return nil
	})
}

func TestSoftDeleteUser(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	var id uuid.UUID
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		v, err := repo.InsertUser(ctx, tx, "del@example.com", "Del")
		if err != nil {
			return err
		}
		id = v.ID
		return nil
	})

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		return repo.SoftDeleteUser(ctx, tx, id)
	})

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		got, err := repo.GetUser(ctx, tx, id)
		if err != nil {
			return err
		}
		if got.DeletedAt == nil {
			t.Fatalf("DeletedAt should be set")
		}
		return nil
	})

	// Second soft-delete is a no-op → ErrNoRows.
	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		err := repo.SoftDeleteUser(ctx, tx, id)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows, got %v", err)
		}
		return nil
	})
}

func TestGetUser_NotFound(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	pool := openSuperPool(t, ctx, tdb.SuperDSN)
	defer pool.Close()

	mustTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.GetUser(ctx, tx, uuid.New())
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows, got %v", err)
		}
		_, err = repo.GetUserByEmail(ctx, tx, "missing@example.com")
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on email lookup, got %v", err)
		}
		return nil
	})
}
