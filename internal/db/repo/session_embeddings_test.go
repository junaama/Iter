//go:build integration

package repo_test

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
)

// randomUnitVec returns a deterministic random unit vector of dim D.
// Using a seeded rand makes test failures reproducible.
func randomUnitVec(r *rand.Rand, d int) []float32 {
	v := make([]float32, d)
	var norm float64
	for i := range v {
		x := r.NormFloat64()
		v[i] = float32(x)
		norm += x * x
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

func TestEmbeddings_UpsertReplaces(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "emb-upsert")

	r := rand.New(rand.NewSource(1))
	v1 := randomUnitVec(r, repo.EmbeddingDim)
	v2 := randomUnitVec(r, repo.EmbeddingDim)

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := repo.UpsertEmbedding(ctx, tx, sessionID, tenantID, v1, "text-embedding-3-small"); err != nil {
			return err
		}
		// Second upsert with a different vector — should replace, not duplicate.
		got, err := repo.UpsertEmbedding(ctx, tx, sessionID, tenantID, v2, "text-embedding-3-small")
		if err != nil {
			return err
		}
		if len(got.Vec) != repo.EmbeddingDim {
			t.Fatalf("expected dim %d, got %d", repo.EmbeddingDim, len(got.Vec))
		}
		// Confirm row count is 1 (no duplicate).
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM session_embeddings WHERE session_id=$1", sessionID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("expected 1 row after upsert, got %d", n)
		}
		// GetEmbeddingForSession returns the latest values.
		e, err := repo.GetEmbeddingForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if !floatSlicesClose(e.Vec, v2, 1e-5) {
			t.Fatalf("GetEmbeddingForSession: stored vector != v2")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestEmbeddings_UpsertValidation(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "emb-val")

	r := rand.New(rand.NewSource(2))
	good := randomUnitVec(r, repo.EmbeddingDim)

	cases := []struct {
		name      string
		sessionID uuid.UUID
		tenantID  uuid.UUID
		vec       []float32
		model     string
	}{
		{"nil session", uuid.Nil, tenantID, good, "m"},
		{"nil tenant", sessionID, uuid.Nil, good, "m"},
		{"empty model", sessionID, tenantID, good, ""},
		{"bad dim", sessionID, tenantID, []float32{1, 2, 3}, "m"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
				_, err := repo.UpsertEmbedding(ctx, tx, tc.sessionID, tc.tenantID, tc.vec, tc.model)
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

func TestEmbeddings_GetMissing(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _, sessionID := seedSessionFor(ctx, t, tdb, "emb-missing")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.GetEmbeddingForSession(ctx, tx, sessionID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("expected pgx.ErrNoRows, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func TestEmbeddings_SearchKNN_DimGuard(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "emb-dim")

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.SearchEmbeddingsKNN(ctx, tx, []float32{1, 2}, 5)
		if err == nil {
			t.Fatalf("expected dim error")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

// SearchKNN_OrderingByDistance: insert 3 vectors at known cosine
// distances from a query and verify they come back in the expected
// order.
func TestEmbeddings_SearchKNN_Ordering(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, userID := seedTenancy(ctx, t, tdb, "emb-order")

	// Construct three vectors with strictly increasing distance to a
	// query (the unit vector along axis 0). We use axis-aligned
	// vectors so the cosine distance is exactly 1 - dot(query, v).
	dim := repo.EmbeddingDim
	mk := func(weight float32, axis int) []float32 {
		v := make([]float32, dim)
		v[0] = weight
		v[axis] = float32(math.Sqrt(float64(1 - weight*weight)))
		return v
	}
	query := make([]float32, dim)
	query[0] = 1.0

	// near: cos dist 1 - 0.95 = 0.05
	// mid:  cos dist 1 - 0.7  = 0.3
	// far:  cos dist 1 - 0.2  = 0.8
	near := mk(0.95, 1)
	mid := mk(0.7, 2)
	far := mk(0.2, 3)

	// Seed sessions + embeddings in deliberately wrong order so we
	// know the result ordering came from the index, not insertion order.
	sessions := make(map[string]uuid.UUID, 3)
	for label, vec := range map[string][]float32{"far": far, "near": near, "mid": mid} {
		sid := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), userID.String(), zeroTime()))
		sessions[label] = sid
		if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
			_, err := repo.UpsertEmbedding(ctx, tx, sid, tenantID, vec, "test-model")
			return err
		}); err != nil {
			t.Fatalf("upsert %s: %v", label, err)
		}
	}

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		results, err := repo.SearchEmbeddingsKNN(ctx, tx, query, 3)
		if err != nil {
			return err
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		if results[0].SessionID != sessions["near"] {
			t.Fatalf("expected near first, got %s", results[0].SessionID)
		}
		if results[1].SessionID != sessions["mid"] {
			t.Fatalf("expected mid second, got %s", results[1].SessionID)
		}
		if results[2].SessionID != sessions["far"] {
			t.Fatalf("expected far last, got %s", results[2].SessionID)
		}
		// Similarities strictly decreasing.
		for i := 1; i < len(results); i++ {
			if results[i].Similarity >= results[i-1].Similarity {
				t.Fatalf("similarity not monotonic: %.4f, %.4f", results[i-1].Similarity, results[i].Similarity)
			}
		}
		// Similarities in [0,1] for our axis-aligned vectors.
		for _, r := range results {
			if r.Similarity < 0 || r.Similarity > 1+1e-6 {
				t.Fatalf("similarity out of [0,1]: %.4f", r.Similarity)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SearchKNN: %v", err)
	}
}

// SearchKNN_RLSScoped: 50 vectors under tenant A + 50 under tenant B.
// Querying under tenant A must never return a tenant-B session.
func TestEmbeddings_SearchKNN_RLSScoped(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantA, userA := seedTenancy(ctx, t, tdb, "emb-rls-a")
	tenantB, userB := seedTenancy(ctx, t, tdb, "emb-rls-b")

	r := rand.New(rand.NewSource(42))
	aSessions := make(map[uuid.UUID]struct{}, 50)

	for i := 0; i < 50; i++ {
		sid := uuid.MustParse(tdb.SeedSession(ctx, t, tenantA.String(), userA.String(), zeroTime()))
		aSessions[sid] = struct{}{}
		vec := randomUnitVec(r, repo.EmbeddingDim)
		if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
			_, err := repo.UpsertEmbedding(ctx, tx, sid, tenantA, vec, "m")
			return err
		}); err != nil {
			t.Fatalf("seed A: %v", err)
		}
	}
	for i := 0; i < 50; i++ {
		sid := uuid.MustParse(tdb.SeedSession(ctx, t, tenantB.String(), userB.String(), zeroTime()))
		vec := randomUnitVec(r, repo.EmbeddingDim)
		if err := db.WithTenant(ctx, tdb.AppPool, tenantB.String(), func(ctx context.Context, tx pgx.Tx) error {
			_, err := repo.UpsertEmbedding(ctx, tx, sid, tenantB, vec, "m")
			return err
		}); err != nil {
			t.Fatalf("seed B: %v", err)
		}
	}

	q := randomUnitVec(r, repo.EmbeddingDim)
	if err := db.WithTenant(ctx, tdb.AppPool, tenantA.String(), func(ctx context.Context, tx pgx.Tx) error {
		results, err := repo.SearchEmbeddingsKNN(ctx, tx, q, 50)
		if err != nil {
			return err
		}
		if len(results) == 0 {
			t.Fatalf("expected hits under tenant A")
		}
		for _, r := range results {
			if _, ok := aSessions[r.SessionID]; !ok {
				t.Fatalf("RLS leak: SearchKNN returned %s (not in tenant A)", r.SessionID)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SearchKNN under A: %v", err)
	}
}

// SearchKNN_DefaultK exercises the k<=0 branch.
func TestEmbeddings_SearchKNN_DefaultK(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()
	tenantID, _ := seedTenancy(ctx, t, tdb, "emb-defk")

	q := randomUnitVec(rand.New(rand.NewSource(3)), repo.EmbeddingDim)
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		results, err := repo.SearchEmbeddingsKNN(ctx, tx, q, 0)
		if err != nil {
			return err
		}
		if len(results) > 10 {
			t.Fatalf("default k should cap at 10, got %d", len(results))
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}

func floatSlicesClose(a, b []float32, eps float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		d := a[i] - b[i]
		if d < 0 {
			d = -d
		}
		if d > eps {
			return false
		}
	}
	return true
}

// zeroTime returns the zero time; helper that makes test call sites
// read more nicely when SeedSession's startedAt arg is intentionally
// the default.
func zeroTime() time.Time { return time.Time{} }
