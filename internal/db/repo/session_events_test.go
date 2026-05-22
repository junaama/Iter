//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestSessionEvents_AppendAndList(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "ev")

	// Create a session to attach events to.
	var sessionID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		sessionID = s.ID
		return nil
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Insert events out-of-order; ListSessionEvents must return them
	// in occurred_at ASC order regardless.
	base := time.Now().UTC().Truncate(time.Microsecond)
	occurredOrder := []time.Time{
		base.Add(30 * time.Millisecond),
		base.Add(10 * time.Millisecond),
		base.Add(20 * time.Millisecond),
		base.Add(40 * time.Millisecond),
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i, ts := range occurredOrder {
			payload := map[string]any{"i": i, "ts": ts.UnixNano()}
			_, err := repo.AppendSessionEvent(ctx, tx, repo.SessionEventRow{
				SessionID:  sessionID,
				TenantID:   tenantID,
				EventType:  contracts.EventTurnCompleted,
				Payload:    payload,
				OccurredAt: ts,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.ListSessionEvents(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if len(got) != 4 {
			t.Fatalf("len = %d, want 4", len(got))
		}
		// Verify ASC order.
		for i := 1; i < len(got); i++ {
			if got[i].OccurredAt.Before(got[i-1].OccurredAt) {
				t.Fatalf("events not in ASC order at %d: %v before %v", i, got[i].OccurredAt, got[i-1].OccurredAt)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestSessionEvents_DefaultsOccurredAt(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "def")

	var sessionID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		sessionID = s.ID
		ev, err := repo.AppendSessionEvent(ctx, tx, repo.SessionEventRow{
			SessionID: sessionID,
			TenantID:  tenantID,
			EventType: contracts.EventPromptSent,
			// OccurredAt zero → server-side now()
		})
		if err != nil {
			return err
		}
		if ev.OccurredAt.IsZero() {
			t.Fatal("OccurredAt should be server-defaulted, got zero")
		}
		return nil
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestSessionEvents_RejectsInvalidType(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "rej")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.AppendSessionEvent(ctx, tx, repo.SessionEventRow{
			SessionID: uuid.New(),
			TenantID:  tenantID,
			EventType: contracts.EventType("not_a_real_event"),
		})
		if err == nil {
			t.Fatal("expected validation error")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestSessionEvents_CountAndListByTypeSince(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "cnt")

	var sessionID uuid.UUID
	base := time.Now().UTC().Truncate(time.Microsecond)
	cutoff := base.Add(15 * time.Millisecond)

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		sessionID = s.ID
		// 2 turn_completed before cutoff, 3 after.
		for i := 0; i < 5; i++ {
			ts := base.Add(time.Duration(i) * 10 * time.Millisecond)
			_, err := repo.AppendSessionEvent(ctx, tx, repo.SessionEventRow{
				SessionID:  sessionID,
				TenantID:   tenantID,
				EventType:  contracts.EventTurnCompleted,
				OccurredAt: ts,
			})
			if err != nil {
				return err
			}
		}
		// And one different type after cutoff that must NOT count.
		_, err = repo.AppendSessionEvent(ctx, tx, repo.SessionEventRow{
			SessionID:  sessionID,
			TenantID:   tenantID,
			EventType:  contracts.EventToolCall,
			OccurredAt: base.Add(45 * time.Millisecond),
		})
		return err
	}); err != nil {
		t.Fatalf("seed events: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		n, err := repo.CountSessionEventsByTypeSince(ctx, tx, sessionID, contracts.EventTurnCompleted, cutoff)
		if err != nil {
			return err
		}
		// turn_completed at 0,10,20,30,40 → after >=15: 20,30,40 → 3.
		if n != 3 {
			t.Fatalf("Count = %d, want 3", n)
		}
		evs, err := repo.ListSessionEventsByTypeSince(ctx, tx, sessionID, contracts.EventTurnCompleted, cutoff)
		if err != nil {
			return err
		}
		if len(evs) != 3 {
			t.Fatalf("ListByTypeSince len = %d, want 3", len(evs))
		}
		return nil
	}); err != nil {
		t.Fatalf("count/list: %v", err)
	}
}

func TestSessionEvents_CascadeOnSessionDelete(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "casc")

	var sessionID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		sessionID = s.ID
		for i := 0; i < 3; i++ {
			_, err := repo.AppendSessionEvent(ctx, tx, repo.SessionEventRow{
				SessionID: sessionID,
				TenantID:  tenantID,
				EventType: contracts.EventTurnCompleted,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.DeleteSession(ctx, tx, sessionID)
	}); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.ListSessionEvents(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			t.Fatalf("cascade broken: %d events survived", len(got))
		}
		return nil
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
