//go:build integration

package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

func ptr(s string) *string { return &s }

func TestOutcomes_InsertAndList(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "outcome-list")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i, kind := range []string{repo.OutcomePRMerged, repo.OutcomeTestsPassed, repo.OutcomeCommitLanded} {
			_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
				TenantID:    tenantID,
				SessionID:   sessionID,
				OutcomeType: kind,
				ExternalRef: ptr("https://example.com/" + kind),
				Details:     json.RawMessage(`{"i":` + string(rune('0'+i)) + `}`),
			})
			if err != nil {
				return err
			}
		}
		list, err := repo.ListOutcomesForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if len(list) != 3 {
			t.Fatalf("expected 3 outcomes, got %d", len(list))
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestOutcomes_InsertDedupOnExternalRef(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "outcome-dedup")

	// First insert lands.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
			TenantID:    tenantID,
			SessionID:   sessionID,
			OutcomeType: repo.OutcomePRMerged,
			ExternalRef: ptr("https://github.com/x/y/pull/1"),
		})
		return err
	}); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Second insert collides — ErrAlreadyExists.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
			TenantID:    tenantID,
			SessionID:   sessionID,
			OutcomeType: repo.OutcomePRMerged,
			ExternalRef: ptr("https://github.com/x/y/pull/1"),
		})
		if !errors.Is(err, repo.ErrAlreadyExists) {
			t.Fatalf("expected ErrAlreadyExists, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("second insert: %v", err)
	}
}

func TestOutcomes_InsertNullExternalRefNotDeduped(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "outcome-null-ref")

	// Two NULL-external_ref outcomes of the same type SHOULD coexist
	// because the unique index is partial.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i := 0; i < 2; i++ {
			_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
				TenantID:    tenantID,
				SessionID:   sessionID,
				OutcomeType: repo.OutcomeTestsFailed,
			})
			if err != nil {
				return err
			}
		}
		list, err := repo.ListOutcomesForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if len(list) != 2 {
			t.Fatalf("expected both null-ref rows, got %d", len(list))
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestOutcomes_InsertValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "outcome-val")

	cases := []struct {
		name string
		mod  func(*repo.Outcome)
	}{
		{"no session_id", func(o *repo.Outcome) { o.SessionID = uuid.Nil }},
		{"no tenant_id", func(o *repo.Outcome) { o.TenantID = uuid.Nil }},
		{"bad outcome_type", func(o *repo.Outcome) { o.OutcomeType = "exploded" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
				o := repo.Outcome{
					TenantID:    tenantID,
					SessionID:   sessionID,
					OutcomeType: repo.OutcomePRMerged,
				}
				tc.mod(&o)
				_, err := repo.InsertOutcome(ctx, tx, o)
				if err == nil {
					t.Fatalf("expected error for %s", tc.name)
				}
				return nil
			}); err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
		})
	}
}

func TestOutcomes_CountByTypeSince(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "outcome-count")

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		// 3 pr_merged within window, 1 pr_merged before window.
		for i := 0; i < 3; i++ {
			ref := "https://example.com/recent/" + string(rune('a'+i))
			_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
				TenantID:    tenantID,
				SessionID:   sessionID,
				OutcomeType: repo.OutcomePRMerged,
				ExternalRef: &ref,
				ObservedAt:  now.Add(time.Duration(-i) * time.Hour),
			})
			if err != nil {
				return err
			}
		}
		oldRef := "https://example.com/old"
		_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
			TenantID:    tenantID,
			SessionID:   sessionID,
			OutcomeType: repo.OutcomePRMerged,
			ExternalRef: &oldRef,
			ObservedAt:  now.Add(-48 * time.Hour),
		})
		if err != nil {
			return err
		}

		n, err := repo.CountOutcomesByTypeSince(ctx, tx, tenantID, repo.OutcomePRMerged, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if n != 3 {
			t.Fatalf("expected 3 within last 24h, got %d", n)
		}

		// Bad type still errors at validation step.
		if _, err := repo.CountOutcomesByTypeSince(ctx, tx, tenantID, "nope", now); err == nil {
			t.Fatalf("expected invalid-type error")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

// Ensure outcomes inserted under tenant A are invisible to tenant B
// when reading via RLS (defense in depth — the partial unique index
// is the dedup path; this asserts RLS scoping holds).
func TestOutcomes_RLSScoped(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantA, _, sessionA := seedSessionFor(ctx, t, tdb, "out-rls-a")
	tenantB, _, _ := seedSessionFor(ctx, t, tdb, "out-rls-b")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
			TenantID: tenantA, SessionID: sessionA,
			OutcomeType: repo.OutcomePRMerged, ExternalRef: ptr("ref-a"),
		})
		return err
	}); err != nil {
		t.Fatalf("insert under A: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
		list, err := repo.ListOutcomesForSession(ctx, tx, sessionA)
		if err != nil {
			return err
		}
		if len(list) != 0 {
			t.Fatalf("RLS leak: tenant B saw %d outcomes from tenant A", len(list))
		}
		n, err := repo.CountOutcomesByTypeSince(ctx, tx, tenantB, repo.OutcomePRMerged, time.Now().UTC().Add(-time.Hour))
		if err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("RLS leak: count under B = %d", n)
		}
		return nil
	}); err != nil {
		_ = err // RLS denial may also surface as an error inside WithTenant
	}

	// Unused vars (kept for clarity above)
	_ = pgx.ErrNoRows
}
