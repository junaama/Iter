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

// seedSessionFor mints a tenant + user + session under tenantName.
// Returns (tenantID, userID, sessionID) as parsed UUIDs.
func seedSessionFor(ctx context.Context, t *testing.T, tdb *dbtest.TestDB, tenantName string) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	tenantID, userID := seedTenancy(ctx, t, tdb, tenantName)
	sessionID := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), userID.String(), time.Time{}))
	return tenantID, userID, sessionID
}

func newScore(tenantID, sessionID uuid.UUID, version string, composite float64) repo.Score {
	return repo.Score{
		TenantID:          tenantID,
		SessionID:         sessionID,
		ScorerVersion:     version,
		CompositeScore:    composite,
		Signals:           json.RawMessage(`{"durability_7d":0.6}`),
		ContributorWeight: 0.5,
	}
}

func TestScores_InsertIdempotent(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "score-idem")

	var first repo.Score
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertScore(ctx, tx, newScore(tenantID, sessionID, "v1", 0.7))
		if err != nil {
			return err
		}
		first = s
		return nil
	}); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	var second repo.Score
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		// Re-insert with same (session_id, scorer_version) but different score.
		s, err := repo.InsertScore(ctx, tx, newScore(tenantID, sessionID, "v1", 0.99))
		if err != nil {
			return err
		}
		second = s
		return nil
	}); err != nil {
		t.Fatalf("second insert: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("ON CONFLICT DO NOTHING expected same id; got %s -> %s", first.ID, second.ID)
	}
	if second.CompositeScore != 0.7 {
		t.Fatalf("expected preserved composite_score 0.7, got %.3f", second.CompositeScore)
	}

	// Sanity: only one row exists for (session_id, scorer_version).
	var count int
	if err := tdb.Super.QueryRowContext(ctx,
		"SELECT count(*) FROM session_scores WHERE session_id=$1 AND scorer_version=$2",
		sessionID, "v1",
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after idempotent insert, got %d", count)
	}
}

func TestScores_InsertValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "score-val")

	cases := []struct {
		name string
		mod  func(*repo.Score)
	}{
		{"no session_id", func(s *repo.Score) { s.SessionID = uuid.Nil }},
		{"no tenant_id", func(s *repo.Score) { s.TenantID = uuid.Nil }},
		{"no scorer_version", func(s *repo.Score) { s.ScorerVersion = "" }},
		{"composite < 0", func(s *repo.Score) { s.CompositeScore = -0.1 }},
		{"composite > 1", func(s *repo.Score) { s.CompositeScore = 1.5 }},
		{"weight > 1", func(s *repo.Score) { s.ContributorWeight = 1.1 }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
				s := newScore(tenantID, sessionID, "v1", 0.5)
				tc.mod(&s)
				_, err := repo.InsertScore(ctx, tx, s)
				if err == nil {
					t.Fatalf("expected validation error for %s", tc.name)
				}
				return nil
			}); err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
		})
	}
}

func TestScores_LatestAndList(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "score-latest")

	base := time.Now().UTC().Truncate(time.Microsecond)
	// Three scores at increasing scored_at — seed directly so we
	// can control timestamps and observe ordering.
	tdb.SeedScore(ctx, t, tenantID.String(), sessionID.String(), "v1", 0.3, base.Add(-2*time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), sessionID.String(), "v2", 0.7, base.Add(-1*time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), sessionID.String(), "v3", 0.5, base)

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		latest, err := repo.LatestScoreForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if latest.ScorerVersion != "v3" {
			t.Fatalf("LatestScoreForSession: expected v3, got %s", latest.ScorerVersion)
		}

		all, err := repo.ListScoresForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if len(all) != 3 {
			t.Fatalf("ListScoresForSession: expected 3, got %d", len(all))
		}
		if all[0].ScorerVersion != "v3" || all[2].ScorerVersion != "v1" {
			t.Fatalf("ListScoresForSession order wrong: %s,%s,%s", all[0].ScorerVersion, all[1].ScorerVersion, all[2].ScorerVersion)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestScores_LatestForSessionMissing(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "score-missing")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.LatestScoreForSession(ctx, tx, sessionID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected pgx.ErrNoRows, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestScores_MeanCompositeForUser(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "score-mean")

	base := time.Now().UTC().Truncate(time.Microsecond)
	// Three sessions, each with two scores. Only the latest per
	// session counts toward the mean.
	for i := 0; i < 3; i++ {
		sid := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), userID.String(), base))
		tdb.SeedScore(ctx, t, tenantID.String(), sid.String(), "v1", 0.1, base.Add(-1*time.Hour))
		// Latest score per session: 0.4, 0.6, 0.8 -> mean 0.6
		tdb.SeedScore(ctx, t, tenantID.String(), sid.String(), "v2", 0.4+float64(i)*0.2, base)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		mean, count, err := repo.MeanCompositeForUser(ctx, tx, userID, base.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if count != 3 {
			t.Fatalf("expected 3 sessions, got %d", count)
		}
		if mean < 0.59 || mean > 0.61 {
			t.Fatalf("expected mean ~0.6, got %.4f", mean)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestScores_MeanCompositeForUserEmpty(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "score-mean-empty")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		mean, count, err := repo.MeanCompositeForUser(ctx, tx, userID, time.Now().UTC().Add(-time.Hour))
		if err != nil {
			return err
		}
		if mean != 0 || count != 0 {
			t.Fatalf("expected (0,0) for empty window, got (%.3f, %d)", mean, count)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestScores_MeanCompositePerDay(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "score-daily")

	// Seed 5 days x 3 sessions, latest score per session 0.5.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for d := 0; d < 5; d++ {
		dayAt := today.Add(time.Duration(-d) * 24 * time.Hour).Add(12 * time.Hour)
		for s := 0; s < 3; s++ {
			sid := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), userID.String(), dayAt))
			tdb.SeedScore(ctx, t, tenantID.String(), sid.String(), "v1", 0.3, dayAt.Add(-time.Hour))
			tdb.SeedScore(ctx, t, tenantID.String(), sid.String(), "v2", 0.5, dayAt)
		}
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		days, err := repo.MeanCompositePerDay(ctx, tx, userID, 10)
		if err != nil {
			return err
		}
		if len(days) != 5 {
			t.Fatalf("expected 5 day buckets, got %d", len(days))
		}
		// Days arrive ASC; each bucket has 3 sessions, mean 0.5.
		for _, d := range days {
			if d.Count != 3 {
				t.Fatalf("expected 3 sessions per day, got %d on %s", d.Count, d.Day)
			}
			if d.Mean < 0.49 || d.Mean > 0.51 {
				t.Fatalf("expected mean ~0.5 on %s, got %.4f", d.Day, d.Mean)
			}
		}
		// Ordering ASC.
		for i := 1; i < len(days); i++ {
			if !days[i].Day.After(days[i-1].Day) {
				t.Fatalf("MeanCompositePerDay should be ASC; got %v then %v", days[i-1].Day, days[i].Day)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestScores_MeanCompositePerDayDefaults(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "score-daily-def")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		days, err := repo.MeanCompositePerDay(ctx, tx, userID, 0) // exercises default
		if err != nil {
			return err
		}
		if len(days) != 0 {
			t.Fatalf("expected empty result, got %d", len(days))
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}
