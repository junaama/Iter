//go:build integration

package repo_test

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

func TestDashboardTeam_MemberAggregatesWindowAndFormerMember(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, activeUserID := seedTenancy(ctx, t, tdb, "team-members")
	formerUserID := uuid.MustParse(tdb.SeedUser(ctx, t, "former@example.com", "Should Hide"))

	base := time.Now().UTC().Truncate(time.Microsecond)
	activeOne := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), activeUserID.String(), base.Add(-2*time.Hour)))
	activeTwo := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), activeUserID.String(), base.Add(-1*time.Hour)))
	oldSession := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), activeUserID.String(), base.AddDate(0, 0, -31)))
	formerSession := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), formerUserID.String(), base.Add(-30*time.Minute)))

	tdb.SeedScore(ctx, t, tenantID.String(), activeOne.String(), "v1", 0.4, base.Add(-90*time.Minute))
	tdb.SeedScore(ctx, t, tenantID.String(), activeOne.String(), "v2", 0.6, base.Add(-80*time.Minute))
	tdb.SeedScore(ctx, t, tenantID.String(), activeTwo.String(), "v1", 0.8, base.Add(-50*time.Minute))
	tdb.SeedScore(ctx, t, tenantID.String(), oldSession.String(), "v1", 1.0, base.AddDate(0, 0, -31))
	tdb.SeedScore(ctx, t, tenantID.String(), formerSession.String(), "v1", 0.2, base.Add(-20*time.Minute))

	if _, err := tdb.Super.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, formerUserID); err != nil {
		t.Fatalf("soft-delete former user: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.ListTeamMemberAggregates(ctx, tx, base.AddDate(0, 0, -30), 100)
		if err != nil {
			return err
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2: %#v", len(got), got)
		}
		if got[0].UserID != activeUserID {
			t.Fatalf("first user = %s, want active user %s", got[0].UserID, activeUserID)
		}
		if got[0].SessionCount30d != 2 {
			t.Fatalf("active count = %d, want 2", got[0].SessionCount30d)
		}
		if got[0].MeanCompositeScore30d == nil || math.Abs(*got[0].MeanCompositeScore30d-0.7) > 0.001 {
			t.Fatalf("active mean = %v, want ~0.7", got[0].MeanCompositeScore30d)
		}
		if got[1].UserID != formerUserID {
			t.Fatalf("second user = %s, want former user %s", got[1].UserID, formerUserID)
		}
		if got[1].DisplayName != "former member" {
			t.Fatalf("former display name = %q, want former member", got[1].DisplayName)
		}
		return nil
	}); err != nil {
		t.Fatalf("ListTeamMemberAggregates: %v", err)
	}
}

func TestDashboardTeam_TopPatternsWindowOrderAndPreview(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, _ := seedTenancy(ctx, t, tdb, "team-patterns")
	now := time.Now().UTC().Truncate(time.Microsecond)

	var strong, weak, old repo.Suggestion
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		strong, err = repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, "first\nprompt\twith   whitespace", randVec(testRand(1))))
		if err != nil {
			return err
		}
		weak, err = repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, "second prompt", randVec(testRand(2))))
		if err != nil {
			return err
		}
		old, err = repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, "old prompt", randVec(testRand(3))))
		return err
	}); err != nil {
		t.Fatalf("seed suggestions: %v", err)
	}

	setSuggestionCounters(t, ctx, tdb, strong.ID, 10, 5, now)
	setSuggestionCounters(t, ctx, tdb, weak.ID, 10, 1, now.Add(-time.Minute))
	setSuggestionCounters(t, ctx, tdb, old.ID, 20, 20, now.AddDate(0, 0, -31))

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.ListTeamTopPatterns(ctx, tx, now.AddDate(0, 0, -30), 10)
		if err != nil {
			return err
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2: %#v", len(got), got)
		}
		if got[0].PatternID != strong.ID || got[1].PatternID != weak.ID {
			t.Fatalf("order = %s, %s; want strong then weak", got[0].PatternID, got[1].PatternID)
		}
		if got[0].UsesCount != 10 || got[0].TenantsUsed != 1 {
			t.Fatalf("strong counters = uses %d tenants %d", got[0].UsesCount, got[0].TenantsUsed)
		}
		if math.Abs(got[0].AvgScore-0.5) > 0.001 {
			t.Fatalf("strong avg score = %.3f, want 0.5", got[0].AvgScore)
		}
		if strings.ContainsAny(got[0].Preview, "\n\t") {
			t.Fatalf("preview should collapse whitespace, got %q", got[0].Preview)
		}
		return nil
	}); err != nil {
		t.Fatalf("ListTeamTopPatterns: %v", err)
	}
}

func TestDashboardTeam_InviteSettingsAndEmptyAggregates(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, _ := seedTenancy(ctx, t, tdb, "team-empty")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		members, err := repo.ListTeamMemberAggregates(ctx, tx, time.Now().UTC().AddDate(0, 0, -30), 100)
		if err != nil {
			return err
		}
		if len(members) != 0 {
			t.Fatalf("empty members len = %d, want 0", len(members))
		}

		patterns, err := repo.ListTeamTopPatterns(ctx, tx, time.Now().UTC().AddDate(0, 0, -30), 10)
		if err != nil {
			return err
		}
		if len(patterns) != 0 {
			t.Fatalf("empty patterns len = %d, want 0", len(patterns))
		}

		invite, err := repo.GetTeamInviteSettings(ctx, tx, tenantID)
		if err != nil {
			return err
		}
		if !invite.Enabled {
			t.Fatal("default invite settings should be enabled")
		}
		if !strings.Contains(invite.InviteLinkTemplate, tenantID.String()) {
			t.Fatalf("invite link should include tenant id, got %q", invite.InviteLinkTemplate)
		}
		return nil
	}); err != nil {
		t.Fatalf("empty/defaults: %v", err)
	}

	if _, err := tdb.Super.ExecContext(ctx, `
		UPDATE tenants
		   SET tenant_settings = '{"team_invites_enabled": false}'::jsonb
		 WHERE id = $1
	`, tenantID); err != nil {
		t.Fatalf("disable invites: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		invite, err := repo.GetTeamInviteSettings(ctx, tx, tenantID)
		if err != nil {
			return err
		}
		if invite.Enabled {
			t.Fatal("invite settings should be disabled")
		}
		return nil
	}); err != nil {
		t.Fatalf("disabled invite settings: %v", err)
	}
}

func setSuggestionCounters(
	t *testing.T,
	ctx context.Context,
	tdb *dbtest.TestDB,
	id uuid.UUID,
	hitCount, acceptCount int,
	lastUsedAt time.Time,
) {
	t.Helper()
	if _, err := tdb.Super.ExecContext(ctx, `
		UPDATE suggestions
		   SET hit_count = $2,
		       accept_count = $3,
		       last_used_at = $4
		 WHERE id = $1
	`, id, hitCount, acceptCount, lastUsedAt); err != nil {
		t.Fatalf("set suggestion counters: %v", err)
	}
}

func testRand(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}
