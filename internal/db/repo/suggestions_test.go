//go:build integration

package repo_test

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

const embedDim = 1536

// randVec produces a deterministic-per-seed []float32 of length embedDim.
// Tests pre-seed rand to keep failures reproducible without bloating the
// test file with literal vectors.
func randVec(r *rand.Rand) []float32 {
	v := make([]float32, embedDim)
	for i := range v {
		v[i] = float32(r.NormFloat64())
	}
	return v
}

func newSuggestion(tenantID uuid.UUID, prompt string, vec []float32) repo.Suggestion {
	return repo.Suggestion{
		TenantID:           tenantID,
		SourcePrompt:       prompt,
		SourceEmbedding:    pgvector.NewVector(vec),
		RefinedPrompt:      "refined: " + prompt,
		EvidenceSessionIDs: []uuid.UUID{},
	}
}

func TestSuggestions_UpsertDedup(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, _ := seedTenancy(ctx, t, tdb, "sugg-dedup")
	r := rand.New(rand.NewSource(1))
	vec := randVec(r)
	prompt := "build a CRUD API"

	var first repo.Suggestion
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, prompt, vec))
		if err != nil {
			return err
		}
		first = s
		return nil
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.HitCount != 0 {
		t.Fatalf("first upsert hit_count = %d, want 0", first.HitCount)
	}

	// Second upsert with the same (tenant_id, source_prompt) — different
	// refined_prompt to confirm the existing row is NOT overwritten.
	var second repo.Suggestion
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		again := newSuggestion(tenantID, prompt, vec)
		again.RefinedPrompt = "DIFFERENT refinement"
		s, err := repo.UpsertSuggestion(ctx, tx, again)
		if err != nil {
			return err
		}
		second = s
		return nil
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("dedup failed: second.ID = %s, first.ID = %s", second.ID, first.ID)
	}
	if second.HitCount != 1 {
		t.Fatalf("second upsert hit_count = %d, want 1", second.HitCount)
	}
	if second.RefinedPrompt != first.RefinedPrompt {
		t.Fatalf("refined_prompt overwritten: got %q, want %q (stable)", second.RefinedPrompt, first.RefinedPrompt)
	}

	// Third upsert — confirm hit_count keeps incrementing.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, prompt, vec))
		if err != nil {
			return err
		}
		if s.HitCount != 2 {
			t.Fatalf("third upsert hit_count = %d, want 2", s.HitCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
}

func TestSuggestions_UpsertValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "sugg-val")
	r := rand.New(rand.NewSource(2))

	cases := []struct {
		name string
		mod  func(s *repo.Suggestion)
	}{
		{"no tenant", func(s *repo.Suggestion) { s.TenantID = uuid.Nil }},
		{"no prompt", func(s *repo.Suggestion) { s.SourcePrompt = "" }},
		{"no refined", func(s *repo.Suggestion) { s.RefinedPrompt = "" }},
		{"empty vector", func(s *repo.Suggestion) { s.SourceEmbedding = pgvector.NewVector(nil) }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
				s := newSuggestion(tenantID, "p", randVec(r))
				tc.mod(&s)
				_, err := repo.UpsertSuggestion(ctx, tx, s)
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

func TestSuggestions_SearchKNN_RLSScoped(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantA, _ := seedTenancy(ctx, t, tdb, "sugg-knn-a")
	tenantB, _ := seedTenancy(ctx, t, tdb, "sugg-knn-b")
	rA := rand.New(rand.NewSource(3))
	rB := rand.New(rand.NewSource(4))

	// Seed 100 suggestions per tenant.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i := 0; i < 100; i++ {
			if _, err := repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantA, "A-prompt-"+string(rune('a'+i%26))+"-"+string(rune('0'+i/26)), randVec(rA))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i := 0; i < 100; i++ {
			if _, err := repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantB, "B-prompt-"+string(rune('a'+i%26))+"-"+string(rune('0'+i/26)), randVec(rB))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	// Query under A's WithTenant — every result must be tenant A's.
	queryVec := randVec(rand.New(rand.NewSource(5)))
	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.SearchSuggestionsKNN(ctx, tx, queryVec, 10)
		if err != nil {
			return err
		}
		if len(got) != 10 {
			t.Fatalf("len = %d, want 10", len(got))
		}
		for i, g := range got {
			if g.TenantID != tenantA {
				t.Fatalf("RLS leak at i=%d: got tenant %s, want %s", i, g.TenantID, tenantA)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SearchKNN under A: %v", err)
	}

	// And under B.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
		got, err := repo.SearchSuggestionsKNN(ctx, tx, queryVec, 10)
		if err != nil {
			return err
		}
		if len(got) != 10 {
			t.Fatalf("len = %d, want 10", len(got))
		}
		for i, g := range got {
			if g.TenantID != tenantB {
				t.Fatalf("RLS leak at i=%d: got tenant %s, want %s", i, g.TenantID, tenantB)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SearchKNN under B: %v", err)
	}
}

func TestSuggestions_SearchKNN_Validation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "sugg-knn-val")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := repo.SearchSuggestionsKNN(ctx, tx, nil, 5); err == nil {
			t.Fatal("expected error on empty query vector")
		}
		// k <= 0 should fall back, not error
		if _, err := repo.SearchSuggestionsKNN(ctx, tx, randVec(rand.New(rand.NewSource(6))), 0); err != nil {
			t.Fatalf("k=0 fallback failed: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestSuggestions_IncrementCounters(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "sugg-incr")
	r := rand.New(rand.NewSource(7))

	var id uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, "p", randVec(r)))
		if err != nil {
			return err
		}
		id = s.ID
		return nil
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if err := repo.IncrementHitCount(ctx, tx, id); err != nil {
			return err
		}
		if err := repo.IncrementAcceptCount(ctx, tx, id); err != nil {
			return err
		}
		if err := repo.IncrementAcceptCount(ctx, tx, id); err != nil {
			return err
		}
		// Missing id branch.
		err1 := repo.IncrementHitCount(ctx, tx, uuid.New())
		if !errors.Is(err1, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows, got %v", err1)
		}
		err2 := repo.IncrementAcceptCount(ctx, tx, uuid.New())
		if !errors.Is(err2, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows, got %v", err2)
		}
		return nil
	}); err != nil {
		t.Fatalf("incr: %v", err)
	}

	// Re-read and check the counters.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		top, err := repo.TopByAcceptance(ctx, tx, time.Now().Add(-time.Hour), 5)
		if err != nil {
			return err
		}
		if len(top) != 1 {
			t.Fatalf("TopByAcceptance len = %d, want 1", len(top))
		}
		if top[0].AcceptCount != 2 {
			t.Fatalf("accept_count = %d, want 2", top[0].AcceptCount)
		}
		if top[0].HitCount != 1 {
			t.Fatalf("hit_count = %d, want 1", top[0].HitCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
}

func TestSuggestions_TopByAcceptance_LimitFallback(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "sugg-top")
	r := rand.New(rand.NewSource(8))

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		for i := 0; i < 3; i++ {
			if _, err := repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, "prompt-"+string(rune('a'+i)), randVec(r))); err != nil {
				return err
			}
		}
		// limit <= 0 falls back to 10
		top, err := repo.TopByAcceptance(ctx, tx, time.Now().Add(-time.Hour), 0)
		if err != nil {
			return err
		}
		if len(top) != 3 {
			t.Fatalf("TopByAcceptance len = %d, want 3", len(top))
		}
		// since-filter excludes everything
		top2, err := repo.TopByAcceptance(ctx, tx, time.Now().Add(time.Hour), 10)
		if err != nil {
			return err
		}
		if len(top2) != 0 {
			t.Fatalf("future-cutoff len = %d, want 0", len(top2))
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestSuggestions_Delete(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "sugg-del")
	r := rand.New(rand.NewSource(9))

	var id uuid.UUID
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		s, err := repo.UpsertSuggestion(ctx, tx, newSuggestion(tenantID, "p", randVec(r)))
		if err != nil {
			return err
		}
		id = s.ID
		return nil
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		return repo.DeleteSuggestion(ctx, tx, id)
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Second delete returns ErrNoRows.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		err := repo.DeleteSuggestion(ctx, tx, id)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected ErrNoRows, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestSuggestionHash_Stable(t *testing.T) {
	tID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	h1 := repo.SuggestionHash(tID, "hello")
	h2 := repo.SuggestionHash(tID, "hello")
	if string(h1) != string(h2) {
		t.Fatalf("hash not stable: %x vs %x", h1, h2)
	}
	if len(h1) != 32 {
		t.Fatalf("hash len = %d, want 32", len(h1))
	}
	other := repo.SuggestionHash(tID, "hello!")
	if string(h1) == string(other) {
		t.Fatal("hash collision on different prompt")
	}
	otherTenant := repo.SuggestionHash(uuid.New(), "hello")
	if string(h1) == string(otherTenant) {
		t.Fatal("hash collision on different tenant")
	}
}
