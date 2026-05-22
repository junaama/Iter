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

func newStack(tenantID, userID uuid.UUID, name string) repo.Stack {
	notes := "ship faster"
	return repo.Stack{
		TenantID:       tenantID,
		UserID:         userID,
		Name:           name,
		Harnesses:      []string{"claude_code"},
		Skills:         []string{"frontend-design"},
		Docs:           []string{"https://example.com/style"},
		Notes:          &notes,
		Classification: repo.ClassificationClean,
	}
}

func TestStacks_CRUDRoundTrip(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "stack-rt")

	var created repo.Stack
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.CreateStack(ctx, tx, newStack(tenantID, userID, "my-stack"))
		if err != nil {
			return err
		}
		created = s
		return nil
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("CreateStack: empty id")
	}
	if len(created.Harnesses) != 1 || created.Harnesses[0] != "claude_code" {
		t.Fatalf("Harnesses round-trip mismatch: %v", created.Harnesses)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.GetStack(ctx, tx, created.ID)
		if err != nil {
			return err
		}
		if got.Name != "my-stack" {
			t.Fatalf("GetStack name = %q", got.Name)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetStack: %v", err)
	}

	// Update
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		updated := created
		updated.Name = "renamed"
		updated.Harnesses = []string{"codex", "claude_code"}
		updated.Classification = repo.ClassificationStrippable
		return repo.UpdateStack(ctx, tx, updated)
	}); err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.GetStack(ctx, tx, created.ID)
		if err != nil {
			return err
		}
		if got.Name != "renamed" || got.Classification != repo.ClassificationStrippable {
			t.Fatalf("UpdateStack didn't apply: %+v", got)
		}
		if len(got.Harnesses) != 2 {
			t.Fatalf("Harnesses len = %d", len(got.Harnesses))
		}
		return nil
	}); err != nil {
		t.Fatalf("verify update: %v", err)
	}

	// ListByUser
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		list, err := repo.ListByUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if len(list) != 1 {
			t.Fatalf("ListByUser len = %d", len(list))
		}
		return nil
	}); err != nil {
		t.Fatalf("ListByUser: %v", err)
	}

	// Delete
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.DeleteStack(ctx, tx, created.ID)
	}); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		err := repo.DeleteStack(ctx, tx, created.ID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on second delete, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestStacks_CreateValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "stack-val")

	cases := []struct {
		name string
		mod  func(s *repo.Stack)
	}{
		{"no tenant", func(s *repo.Stack) { s.TenantID = uuid.Nil }},
		{"no user", func(s *repo.Stack) { s.UserID = uuid.Nil }},
		{"empty name", func(s *repo.Stack) { s.Name = "" }},
		{"bad classification", func(s *repo.Stack) { s.Classification = "garbage" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
				s := newStack(tenantID, userID, "x")
				tc.mod(&s)
				_, err := repo.CreateStack(ctx, tx, s)
				if err == nil {
					t.Fatal("expected validation error")
				}
				return nil
			}); err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
		})
	}
}

func TestStacks_UpdateValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "stack-upd-val")

	var created repo.Stack
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.CreateStack(ctx, tx, newStack(tenantID, userID, "x"))
		if err != nil {
			return err
		}
		created = s
		return nil
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	cases := []struct {
		name string
		mod  func(s *repo.Stack)
	}{
		{"no id", func(s *repo.Stack) { s.ID = uuid.Nil }},
		{"empty name", func(s *repo.Stack) { s.Name = "" }},
		{"bad classification", func(s *repo.Stack) { s.Classification = "garbage" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
				s := created
				tc.mod(&s)
				err := repo.UpdateStack(ctx, tx, s)
				if err == nil {
					t.Fatal("expected validation error")
				}
				return nil
			}); err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
		})
	}

	// Update of nonexistent id returns ErrNoRows.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s := created
		s.ID = uuid.New()
		err := repo.UpdateStack(ctx, tx, s)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on missing id, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestStacks_RLSScope_CrossTenantInvisible(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantA, userA := seedTenancy(ctx, t, tdb, "stack-a")
	tenantB, userB := seedTenancy(ctx, t, tdb, "stack-b")

	var idA uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.CreateStack(ctx, tx, newStack(tenantA, userA, "a-stack"))
		if err != nil {
			return err
		}
		idA = s.ID
		return nil
	}); err != nil {
		t.Fatalf("create A: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.GetStack(ctx, tx, idA)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("RLS leak: B saw A's stack: %v", err)
		}
		list, err := repo.ListByUser(ctx, tx, userB)
		if err != nil {
			return err
		}
		if len(list) != 0 {
			t.Fatalf("B sees stacks: %d", len(list))
		}
		// Also: B can't delete or update A's stack.
		if err := repo.DeleteStack(ctx, tx, idA); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on cross-tenant delete, got %v", err)
		}
		bogusUpdate := repo.Stack{ID: idA, Name: "x", Classification: repo.ClassificationClean}
		if err := repo.UpdateStack(ctx, tx, bogusUpdate); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on cross-tenant update, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant B: %v", err)
	}
}

func TestStacks_ListByUser_OrderAndEmpty(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "stack-list")

	// Empty case first.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		list, err := repo.ListByUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if len(list) != 0 {
			t.Fatalf("empty list expected, got %d", len(list))
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	// Two stacks, second created later — second should appear first.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.CreateStack(ctx, tx, newStack(tenantID, userID, "first"))
		return err
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.CreateStack(ctx, tx, newStack(tenantID, userID, "second"))
		return err
	}); err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		list, err := repo.ListByUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if len(list) != 2 {
			t.Fatalf("len = %d, want 2", len(list))
		}
		if list[0].Name != "second" {
			t.Fatalf("order wrong: %v", []string{list[0].Name, list[1].Name})
		}
		return nil
	}); err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
}
