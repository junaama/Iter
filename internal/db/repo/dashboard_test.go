//go:build integration

package repo_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

func TestDashboardMe_LoadWeightedTrendAndRecent(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, userID := seedTenancy(ctx, t, tdb, "dash-me")
	otherUserID := uuid.MustParse(tdb.SeedUser(ctx, t, "other-dash@example.com", "Other User"))
	tdb.SeedMembership(ctx, t, tenantID.String(), otherUserID.String(), repo.RoleMember)
	now := time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC)
	today := now.Truncate(24 * time.Hour)

	longPrompt := strings.Repeat("x", repo.DashboardPromptPreviewChars+1)
	unscoredID := seedDashboardSession(ctx, t, tdb, tenantID, userID, now.Add(-1*time.Hour), longPrompt)
	highID := seedDashboardSession(ctx, t, tdb, tenantID, userID, now.Add(-2*time.Hour), "high score")
	lowID := seedDashboardSession(ctx, t, tdb, tenantID, userID, now.Add(-3*time.Hour), "low score")
	_ = unscoredID

	seedDashboardScore(ctx, t, tdb, tenantID, highID, "v1", 1.0, 0.8, today.Add(9*time.Hour))
	seedDashboardScore(ctx, t, tdb, tenantID, lowID, "v1", 0.5, 0.2, today.Add(8*time.Hour))
	seedDashboardScore(ctx, t, tdb, tenantID, lowID, "v2", 0.0, 0.2, today.Add(10*time.Hour))

	// Same-tenant, different-user row must be filtered out by user_id.
	otherID := seedDashboardSession(ctx, t, tdb, tenantID, otherUserID, now, "other user")
	seedDashboardScore(ctx, t, tdb, tenantID, otherID, "v1", 0.1, 1.0, today.Add(11*time.Hour))

	var got repo.DashboardMe
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = repo.LoadDashboardMe(ctx, tx, userID, 14, 2, now)
		return err
	}); err != nil {
		t.Fatalf("LoadDashboardMe: %v", err)
	}

	if got.User.ID != userID || got.User.Email != "dash-me@example.com" {
		t.Fatalf("user projection mismatch: %+v", got.User)
	}
	if len(got.Trend) != 14 {
		t.Fatalf("trend length: got %d want 14", len(got.Trend))
	}
	last := got.Trend[len(got.Trend)-1]
	if last.Day.Format("2006-01-02") != "2026-05-22" {
		t.Fatalf("last trend day: got %s", last.Day)
	}
	if last.SessionCount != 2 {
		t.Fatalf("session count: got %d want 2", last.SessionCount)
	}
	if last.CompositeScore == nil || *last.CompositeScore < 0.799 || *last.CompositeScore > 0.801 {
		t.Fatalf("weighted score: got %v want ~0.8", last.CompositeScore)
	}
	if gap := got.Trend[len(got.Trend)-2]; gap.CompositeScore != nil || gap.SessionCount != 0 {
		t.Fatalf("empty day should be null/0, got %+v", gap)
	}

	if len(got.RecentSessions) != 2 {
		t.Fatalf("recent len: got %d want 2", len(got.RecentSessions))
	}
	if got.RecentSessions[0].ID != unscoredID {
		t.Fatalf("recent order: first id got %s want %s", got.RecentSessions[0].ID, unscoredID)
	}
	if got.RecentSessions[0].CompositeScore != nil {
		t.Fatalf("unscored recent session should have nil score: %+v", got.RecentSessions[0])
	}
	preview := got.RecentSessions[0].RedactedPromptPreview
	if len(preview) != repo.DashboardPromptPreviewChars+3 || !strings.HasSuffix(preview, "...") {
		t.Fatalf("preview length/suffix: len=%d preview=%q", len(preview), preview)
	}
	if got.RecentSessions[1].ID != highID {
		t.Fatalf("recent limit/order should include high score second; got %s want %s", got.RecentSessions[1].ID, highID)
	}
	if got.RecentSessions[1].CompositeScore == nil || *got.RecentSessions[1].CompositeScore != 1.0 {
		t.Fatalf("recent latest score mismatch: %+v", got.RecentSessions[1])
	}
}

func TestDashboardMe_LoadUnder200msWith500RowsPerUser(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, userID := seedTenancy(ctx, t, tdb, "dash-perf")
	otherUserID := uuid.MustParse(tdb.SeedUser(ctx, t, "dash-perf-other@example.com", "Other User"))
	tdb.SeedMembership(ctx, t, tenantID.String(), otherUserID.String(), repo.RoleMember)
	now := time.Date(2026, 5, 22, 15, 0, 0, 0, time.UTC)

	seedDashboardBulk(ctx, t, tdb, tenantID, userID, 500, now)
	seedDashboardBulk(ctx, t, tdb, tenantID, otherUserID, 500, now)

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		// Warm the connection/query path so the measured section is the
		// aggregate itself, not pool checkout or plan setup noise.
		_, err := repo.LoadDashboardMe(ctx, tx, userID, 14, 10, now)
		return err
	}); err != nil {
		t.Fatalf("warm LoadDashboardMe: %v", err)
	}

	var got repo.DashboardMe
	start := time.Now()
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = repo.LoadDashboardMe(ctx, tx, userID, 14, 10, now)
		return err
	}); err != nil {
		t.Fatalf("measured LoadDashboardMe: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Fatalf("LoadDashboardMe with 500 rows/user took %v, want <=200ms", elapsed)
	}
	if len(got.Trend) != 14 || len(got.RecentSessions) != 10 {
		t.Fatalf("shape mismatch: trend=%d recent=%d", len(got.Trend), len(got.RecentSessions))
	}
	for _, s := range got.RecentSessions {
		if s.CompositeScore == nil || *s.CompositeScore != 0.7 {
			t.Fatalf("recent score mismatch: %+v", s)
		}
	}
}

func seedDashboardSession(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	userID uuid.UUID,
	startedAt time.Time,
	prompt string,
) uuid.UUID {
	t.Helper()
	var id string
	err := tdb.Super.QueryRowContext(ctx, `
		INSERT INTO sessions (
		  tenant_id, user_id, harness, model, tools,
		  started_at, redacted_prompt, classification
		) VALUES ($1, $2, 'claude_code', 'm', ARRAY[]::text[], $3, $4, 'clean')
		RETURNING id
	`, tenantID.String(), userID.String(), startedAt, prompt).Scan(&id)
	if err != nil {
		t.Fatalf("seed dashboard session: %v", err)
	}
	return uuid.MustParse(id)
}

func seedDashboardScore(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	version string,
	composite float64,
	weight float64,
	scoredAt time.Time,
) {
	t.Helper()
	if _, err := tdb.Super.ExecContext(ctx, `
		INSERT INTO session_scores (
		  session_id, tenant_id, scorer_version, composite_score,
		  signals, contributor_weight, scored_at
		) VALUES ($1, $2, $3, $4, '{}'::jsonb, $5, $6)
	`, sessionID.String(), tenantID.String(), version, composite, weight, scoredAt); err != nil {
		t.Fatalf("seed dashboard score: %v", err)
	}
}

func seedDashboardBulk(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	userID uuid.UUID,
	count int,
	now time.Time,
) {
	t.Helper()
	_, err := tdb.Super.ExecContext(ctx, `
		WITH generated AS (
		  SELECT gen_random_uuid() AS id, g
		    FROM generate_series(1, $3::int) AS g
		),
		inserted_sessions AS (
		  INSERT INTO sessions (
		    id, tenant_id, user_id, harness, model, tools,
		    started_at, redacted_prompt, classification
		  )
		  SELECT id,
		         $1,
		         $2,
		         'claude_code',
		         'm',
		         ARRAY[]::text[],
		         $4::timestamptz - (g || ' minutes')::interval,
		         'prompt ' || g::text,
		         'clean'
		    FROM generated
		  RETURNING id, started_at
		)
		INSERT INTO session_scores (
		  session_id, tenant_id, scorer_version, composite_score,
		  signals, contributor_weight, scored_at
		)
		SELECT id, $1, 'v1', 0.700, '{}'::jsonb, 1.000, started_at + interval '30 seconds'
		  FROM inserted_sessions
	`, tenantID.String(), userID.String(), count, now)
	if err != nil {
		t.Fatalf("seed dashboard bulk: %v", err)
	}
}
