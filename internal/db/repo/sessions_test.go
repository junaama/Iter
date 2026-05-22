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

// seedTenancy mints a tenant + user + membership for a sessions test.
// All three rows live on tables without RLS, so we use the superuser
// handle. Returns the (tenant_id, user_id) pair as uuid.UUID.
func seedTenancy(ctx context.Context, t *testing.T, tdb *dbtest.TestDB, name string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, name))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, name+"@example.com", "User-"+name))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), repo.RoleOwner)
	return tenantID, userID
}

func newSession(tenantID, userID uuid.UUID, harness, model string) repo.Session {
	return repo.Session{
		TenantID:       tenantID,
		UserID:         userID,
		Harness:        harness,
		Model:          model,
		Tools:          []string{},
		StartedAt:      time.Now().UTC().Truncate(time.Microsecond),
		RedactedPrompt: "redacted prompt",
		Classification: repo.ClassificationClean,
	}
}

func TestSessions_InsertGet(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, userID := seedTenancy(ctx, t, tdb, "acme")

	var inserted repo.Session
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "claude-sonnet-4"))
		if err != nil {
			return err
		}
		inserted = s
		return nil
	}); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	if inserted.ID == uuid.Nil {
		t.Fatal("InsertSession: empty id")
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.GetSession(ctx, tx, inserted.ID)
		if err != nil {
			return err
		}
		if got.Harness != "claude_code" || got.Model != "claude-sonnet-4" {
			t.Fatalf("GetSession mismatch: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetSession: %v", err)
	}
}

func TestSessions_InsertValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "valid")

	cases := []struct {
		name string
		mod  func(s *repo.Session)
	}{
		{"no tenant_id", func(s *repo.Session) { s.TenantID = uuid.Nil }},
		{"no user_id", func(s *repo.Session) { s.UserID = uuid.Nil }},
		{"no harness", func(s *repo.Session) { s.Harness = "" }},
		{"no prompt", func(s *repo.Session) { s.RedactedPrompt = "" }},
		{"bad classification", func(s *repo.Session) { s.Classification = "leaky" }},
		{"zero started_at", func(s *repo.Session) { s.StartedAt = time.Time{} }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
				s := newSession(tenantID, userID, "claude_code", "m")
				tc.mod(&s)
				_, err := repo.InsertSession(ctx, tx, s)
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

func TestSessions_RLSScope_CrossTenantInvisible(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantA, userA := seedTenancy(ctx, t, tdb, "a")
	tenantB, userB := seedTenancy(ctx, t, tdb, "b")

	// Insert one session per tenant.
	var idA, idB uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantA, userA, "claude_code", "m"))
		if err != nil {
			return err
		}
		idA = s.ID
		return nil
	}); err != nil {
		t.Fatalf("insert under A: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantB, userB, "claude_code", "m"))
		if err != nil {
			return err
		}
		idB = s.ID
		return nil
	}); err != nil {
		t.Fatalf("insert under B: %v", err)
	}

	// As B, fetching A's id must come back as ErrNoRows (RLS hides it).
	if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.GetSession(ctx, tx, idA)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("RLS leak: B saw A's session: %v", err)
		}
		// Recent-by-user for A under B's GUC must be empty.
		got, err := repo.ListRecentByUser(ctx, tx, userA, 10)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			t.Fatalf("RLS leak: B saw %d of A's sessions", len(got))
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant(B): %v", err)
	}

	// And the reverse: A can see its own row but not B's.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := repo.GetSession(ctx, tx, idA); err != nil {
			t.Fatalf("A cannot see its own session: %v", err)
		}
		_, err := repo.GetSession(ctx, tx, idB)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("RLS leak: A saw B's session: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant(A): %v", err)
	}
}

func TestSessions_ListSubagents(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "subs")

	var parentID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		p, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		parentID = p.ID
		for i := 0; i < 3; i++ {
			child := newSession(tenantID, userID, "claude_code", "m")
			child.ParentSessionID = &parentID
			child.StartedAt = time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
			if _, err := repo.InsertSession(ctx, tx, child); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed subagents: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		kids, err := repo.ListSubagents(ctx, tx, parentID)
		if err != nil {
			return err
		}
		if len(kids) != 3 {
			t.Fatalf("ListSubagents len = %d, want 3", len(kids))
		}
		for i, k := range kids {
			if k.ParentSessionID == nil || *k.ParentSessionID != parentID {
				t.Fatalf("subagent[%d] parent_session_id mismatch: %+v", i, k.ParentSessionID)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("ListSubagents: %v", err)
	}
}

func TestSessions_ListWithFilterAndCursor(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "page")

	// Seed 10 sessions, each 5ms apart.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		base := time.Now().UTC().Add(-time.Hour)
		for i := 0; i < 10; i++ {
			s := newSession(tenantID, userID, "claude_code", "m")
			s.StartedAt = base.Add(time.Duration(i) * 5 * time.Millisecond)
			if _, err := repo.InsertSession(ctx, tx, s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Page 1.
	var page1 []repo.Session
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		v, err := repo.ListSessions(ctx, tx, repo.SessionFilter{UserID: &userID}, 4, time.Time{}, uuid.Nil)
		if err != nil {
			return err
		}
		page1 = v
		return nil
	}); err != nil {
		t.Fatalf("ListSessions page1: %v", err)
	}
	if len(page1) != 4 {
		t.Fatalf("page1 len = %d, want 4", len(page1))
	}

	// Insert one more session between pages.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s := newSession(tenantID, userID, "claude_code", "m")
		s.StartedAt = time.Now().UTC().Add(time.Hour) // far in the future
		_, err := repo.InsertSession(ctx, tx, s)
		return err
	}); err != nil {
		t.Fatalf("insert intruder: %v", err)
	}

	// Page 2 — cursor anchored on page1's last row. The new "future"
	// session has started_at > all page1 rows; with a strict
	// (started_at, id) < (cursor) bound, it must NOT appear in page 2.
	last := page1[len(page1)-1]
	var page2 []repo.Session
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		v, err := repo.ListSessions(ctx, tx, repo.SessionFilter{UserID: &userID}, 4, last.StartedAt, last.ID)
		if err != nil {
			return err
		}
		page2 = v
		return nil
	}); err != nil {
		t.Fatalf("ListSessions page2: %v", err)
	}
	seen := map[uuid.UUID]struct{}{}
	for _, r := range page1 {
		seen[r.ID] = struct{}{}
	}
	for _, r := range page2 {
		if _, dup := seen[r.ID]; dup {
			t.Fatalf("page2 contains id from page1: %s", r.ID)
		}
	}
}

func TestSessions_ListSummariesFiltersLatestScoreOutcomeClassification(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "summary")
	otherUserID := uuid.MustParse(tdb.SeedUser(ctx, t, "summary-other@example.com", "Other"))
	tdb.SeedMembership(ctx, t, tenantID.String(), otherUserID.String(), repo.RoleMember)
	base := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

	var keepID, staleScoreID, wrongOutcomeID, dirtyID, otherUserSessionID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		insert := func(user uuid.UUID, harness, classification string, startedAt time.Time) (uuid.UUID, error) {
			s := newSession(tenantID, user, harness, "m")
			s.Classification = classification
			s.StartedAt = startedAt
			inserted, err := repo.InsertSession(ctx, tx, s)
			if err != nil {
				return uuid.Nil, err
			}
			return inserted.ID, nil
		}

		var err error
		keepID, err = insert(userID, "codex", repo.ClassificationClean, base.Add(5*time.Minute))
		if err != nil {
			return err
		}
		staleScoreID, err = insert(userID, "codex", repo.ClassificationClean, base.Add(4*time.Minute))
		if err != nil {
			return err
		}
		wrongOutcomeID, err = insert(userID, "codex", repo.ClassificationClean, base.Add(3*time.Minute))
		if err != nil {
			return err
		}
		dirtyID, err = insert(userID, "codex", repo.ClassificationDirty, base.Add(2*time.Minute))
		if err != nil {
			return err
		}
		otherUserSessionID, err = insert(otherUserID, "codex", repo.ClassificationClean, base.Add(time.Minute))
		if err != nil {
			return err
		}

		for _, outcome := range []struct {
			sessionID uuid.UUID
			typ       string
		}{
			{keepID, repo.OutcomePRMerged},
			{staleScoreID, repo.OutcomePRMerged},
			{wrongOutcomeID, repo.OutcomeTestsFailed},
			{dirtyID, repo.OutcomePRMerged},
			{otherUserSessionID, repo.OutcomePRMerged},
		} {
			_, err := repo.InsertOutcome(ctx, tx, repo.Outcome{
				SessionID:   outcome.sessionID,
				TenantID:    tenantID,
				OutcomeType: outcome.typ,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed summaries: %v", err)
	}

	tdb.SeedScore(ctx, t, tenantID.String(), keepID.String(), "v1", 0.91, base.Add(time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), staleScoreID.String(), "old", 0.95, base.Add(time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), staleScoreID.String(), "latest", 0.20, base.Add(2*time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), wrongOutcomeID.String(), "v1", 0.99, base.Add(time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), dirtyID.String(), "v1", 0.99, base.Add(time.Hour))
	tdb.SeedScore(ctx, t, tenantID.String(), otherUserSessionID.String(), "v1", 0.99, base.Add(time.Hour))

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		minScore := 0.80
		harness := "codex"
		outcome := repo.OutcomePRMerged
		classification := repo.ClassificationClean
		startedAfter := base
		startedBefore := base.Add(time.Hour)
		rows, err := repo.ListSessionSummaries(ctx, tx, repo.SessionSummaryFilter{
			UserID:         &userID,
			Harness:        &harness,
			StartedAfter:   &startedAfter,
			StartedBefore:  &startedBefore,
			MinScore:       &minScore,
			HasOutcome:     &outcome,
			Classification: &classification,
		}, 10, time.Time{}, uuid.Nil)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].Session.ID != keepID {
			t.Fatalf("filtered summaries = %+v, want only %s", rows, keepID)
		}
		if rows[0].LatestScore == nil || rows[0].LatestScore.CompositeScore != 0.91 {
			t.Fatalf("latest score mismatch: %+v", rows[0].LatestScore)
		}
		return nil
	}); err != nil {
		t.Fatalf("ListSessionSummaries filters: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := repo.ListSessionSummaries(ctx, tx, repo.SessionSummaryFilter{ExcludeDirty: true}, 20, time.Time{}, uuid.Nil)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Session.ID == dirtyID {
				t.Fatalf("ExcludeDirty returned dirty session %s", dirtyID)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("ListSessionSummaries ExcludeDirty: %v", err)
	}
}

func TestSessions_ListSummariesCursorStableAcrossInserts(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "summary-page")
	base := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i := 0; i < 5; i++ {
			s := newSession(tenantID, userID, "codex", "m")
			s.StartedAt = base.Add(time.Duration(i) * time.Minute)
			if _, err := repo.InsertSession(ctx, tx, s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed cursor summaries: %v", err)
	}

	var page1 []repo.SessionListRow
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := repo.ListSessionSummaries(ctx, tx, repo.SessionSummaryFilter{UserID: &userID}, 2, time.Time{}, uuid.Nil)
		if err != nil {
			return err
		}
		page1 = rows
		return nil
	}); err != nil {
		t.Fatalf("ListSessionSummaries page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}

	var futureID uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s := newSession(tenantID, userID, "codex", "m")
		s.StartedAt = base.Add(24 * time.Hour)
		inserted, err := repo.InsertSession(ctx, tx, s)
		if err != nil {
			return err
		}
		futureID = inserted.ID
		return nil
	}); err != nil {
		t.Fatalf("insert future summary: %v", err)
	}

	last := page1[len(page1)-1].Session
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		page2, err := repo.ListSessionSummaries(ctx, tx, repo.SessionSummaryFilter{UserID: &userID}, 3, last.StartedAt, last.ID)
		if err != nil {
			return err
		}
		seen := map[uuid.UUID]struct{}{}
		for _, row := range page1 {
			seen[row.Session.ID] = struct{}{}
		}
		for _, row := range page2 {
			if row.Session.ID == futureID {
				t.Fatalf("page2 included newly inserted future session")
			}
			if _, dup := seen[row.Session.ID]; dup {
				t.Fatalf("page2 duplicated page1 session %s", row.Session.ID)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("ListSessionSummaries page2: %v", err)
	}
}

func TestSessions_MarkArchived(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "arch")

	var id uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		id = s.ID
		return nil
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	at := time.Now().UTC().Truncate(time.Second)
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.MarkSessionArchived(ctx, tx, id, at)
	}); err != nil {
		t.Fatalf("MarkArchived: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.GetSession(ctx, tx, id)
		if err != nil {
			return err
		}
		if got.ArchivedAt == nil {
			t.Fatal("ArchivedAt not set")
		}
		if !got.ArchivedAt.Equal(at) {
			t.Fatalf("ArchivedAt = %v, want %v", got.ArchivedAt, at)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Second MarkArchived is a no-op (no error, no change).
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.MarkSessionArchived(ctx, tx, id, time.Now().UTC())
	}); err != nil {
		t.Fatalf("second MarkArchived: %v", err)
	}
}

func TestSessions_Delete(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "del")

	var id uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.InsertSession(ctx, tx, newSession(tenantID, userID, "claude_code", "m"))
		if err != nil {
			return err
		}
		id = s.ID
		return nil
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.DeleteSession(ctx, tx, id)
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		err := repo.DeleteSession(ctx, tx, id)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows on second delete, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify second delete: %v", err)
	}
}
