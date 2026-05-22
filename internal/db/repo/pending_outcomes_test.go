//go:build integration

package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

// Pending outcomes are NOT tenant-scoped — by design (the matching
// tenant is unknown at receive time). Tests use the AppPool directly
// without SET LOCAL because pending_outcomes has no RLS policy.
//
// Each test opens a tx, exercises the helper, and rolls back so the
// shared testcontainer database doesn't accumulate state across tests.

func TestPending_InsertDedupOnDeliveryID(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tx, err := tdb.AppPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	first, err := repo.InsertPending(ctx, tx, repo.PendingOutcome{
		Source:     repo.PendingSourceGitHub,
		DeliveryID: "delivery-A",
		EventType:  "pull_request",
		Payload:    json.RawMessage(`{"a":1}`),
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if first.ID.String() == "" {
		t.Fatalf("expected returned id")
	}

	_, err = repo.InsertPending(ctx, tx, repo.PendingOutcome{
		Source:     repo.PendingSourceGitHub,
		DeliveryID: "delivery-A",
		EventType:  "pull_request",
		Payload:    json.RawMessage(`{"a":2}`),
	})
	if !errors.Is(err, repo.ErrAlreadyExists) {
		t.Fatalf("second insert: want ErrAlreadyExists, got %v", err)
	}
}

func TestPending_ListUnmatchedExcludesMatched(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tx, err := tdb.AppPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	p1, err := repo.InsertPending(ctx, tx, repo.PendingOutcome{
		Source: repo.PendingSourceGitHub, DeliveryID: "d-1", EventType: "push", Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("p1: %v", err)
	}
	_, err = repo.InsertPending(ctx, tx, repo.PendingOutcome{
		Source: repo.PendingSourceGitHub, DeliveryID: "d-2", EventType: "push", Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("p2: %v", err)
	}
	if err := repo.MarkMatched(ctx, tx, p1.ID); err != nil {
		t.Fatalf("mark matched: %v", err)
	}

	rows, err := repo.ListUnmatched(ctx, tx, repo.PendingSourceGitHub, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 unmatched, got %d", len(rows))
	}
	if rows[0].DeliveryID != "d-2" {
		t.Fatalf("expected d-2 unmatched, got %s", rows[0].DeliveryID)
	}
}

func TestPending_DeleteOlderThan(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tx, err := tdb.AppPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := repo.InsertPending(ctx, tx, repo.PendingOutcome{
		Source: repo.PendingSourceGitHub, DeliveryID: "d-old", EventType: "push", Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Backdate the row via the same tx.
	if _, err := tx.Exec(ctx, `UPDATE pending_outcomes SET received_at = now() - interval '30 days' WHERE delivery_id = 'd-old'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	n, err := repo.DeleteOlderThan(ctx, tx, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 deleted, got %d", n)
	}
}

func TestPending_InsertValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tx, err := tdb.AppPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cases := []struct {
		name string
		row  repo.PendingOutcome
	}{
		{"bad source", repo.PendingOutcome{Source: "discord", DeliveryID: "d", EventType: "x"}},
		{"empty delivery_id", repo.PendingOutcome{Source: repo.PendingSourceGitHub, DeliveryID: "", EventType: "x"}},
		{"empty event_type", repo.PendingOutcome{Source: repo.PendingSourceGitHub, DeliveryID: "d", EventType: ""}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.InsertPending(ctx, tx, tc.row); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// ensure unused imports are still tied in.
var _ = pgx.ErrNoRows
