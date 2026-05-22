package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// EmbeddingDim is the fixed embedding dimension shipped in
// migrations/0001_initial.sql (vector(1536)). Callers fast-fail in Go
// before issuing a SQL roundtrip — a dim mismatch is a programmer
// error and the database error message is unhelpful (e.g. "expected
// 1536 dimensions, not N").
const EmbeddingDim = 1536

// Embedding is the storage shape for the session_embeddings table.
// Vec is decoded as []float32 (the wire format pgvector emits).
type Embedding struct {
	SessionID      uuid.UUID `db:"session_id"`
	TenantID       uuid.UUID `db:"tenant_id"`
	Vec            []float32 `db:"embedding"`
	EmbeddingModel string    `db:"embedding_model"`
	CreatedAt      time.Time `db:"created_at"`
}

// ANNResult is one hit from SearchKNN. SessionID is the matched row's
// id; Similarity is 1 - cosine_distance, so it lives in [0, 1] and
// higher is better. Returned in similarity-descending order (i.e.
// closest-first), which matches the SQL `ORDER BY embedding <=> $1`
// (ascending cosine distance).
type ANNResult struct {
	SessionID  uuid.UUID
	Similarity float64
}

// UpsertEmbedding inserts an embedding row or replaces it when one
// already exists for sessionID. ARCHITECTURE.md §9 Step 3 +
// issue 030 (embedding worker) re-embeds sessions when the prompt is
// re-classified or the embedding model upgrades; we replace rather
// than append because the HNSW index is the only consumer and it
// only ever queries the current row.
//
// Dim is checked before any SQL — a mismatch is a programmer error,
// not a runtime input the database should validate.
func UpsertEmbedding(
	ctx context.Context,
	tx pgx.Tx,
	sessionID, tenantID uuid.UUID,
	vec []float32,
	model string,
) (Embedding, error) {
	if sessionID == uuid.Nil {
		return Embedding{}, errors.New("repo.session_embeddings.upsert: session_id required")
	}
	if tenantID == uuid.Nil {
		return Embedding{}, errors.New("repo.session_embeddings.upsert: tenant_id required")
	}
	if model == "" {
		return Embedding{}, errors.New("repo.session_embeddings.upsert: embedding_model required")
	}
	if len(vec) != EmbeddingDim {
		return Embedding{}, fmt.Errorf("repo.session_embeddings.upsert: vector dim %d != %d", len(vec), EmbeddingDim)
	}

	var out Embedding
	err := tx.QueryRow(ctx, `
		INSERT INTO session_embeddings (session_id, tenant_id, embedding, embedding_model)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (session_id) DO UPDATE SET
		  embedding       = EXCLUDED.embedding,
		  embedding_model = EXCLUDED.embedding_model,
		  created_at      = now()
		RETURNING session_id, tenant_id, embedding, embedding_model, created_at
	`, sessionID, tenantID, pgvector.NewVector(vec), model).Scan(
		&out.SessionID, &out.TenantID, scanVecInto(&out.Vec), &out.EmbeddingModel, &out.CreatedAt,
	)
	if err != nil {
		return Embedding{}, fmt.Errorf("repo.session_embeddings.upsert: %w", err)
	}
	return out, nil
}

// GetEmbeddingForSession returns the embedding row for sessionID, or
// pgx.ErrNoRows when none exists (the embedding worker hasn't
// processed the session yet, or it was deleted).
func GetEmbeddingForSession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (Embedding, error) {
	var e Embedding
	err := tx.QueryRow(ctx, `
		SELECT session_id, tenant_id, embedding, embedding_model, created_at
		  FROM session_embeddings
		 WHERE session_id = $1
	`, sessionID).Scan(
		&e.SessionID, &e.TenantID, scanVecInto(&e.Vec), &e.EmbeddingModel, &e.CreatedAt,
	)
	if err != nil {
		return Embedding{}, fmt.Errorf("repo.session_embeddings.get: %w", err)
	}
	return e, nil
}

// SearchKNN runs the HNSW cosine-distance ANN search and returns up to
// k nearest neighbors to queryVec. CRITICAL: the HNSW index was built
// with vector_cosine_ops (migrations/0001) — the matching operator is
// `<=>` (cosine distance), NOT `<->` (L2). Using the wrong operator
// causes a full table scan + wrong ranking.
//
// Similarity is reported as 1 - cosine_distance so callers can apply a
// threshold without needing to remember the operator. Results live in
// [0, 1] for non-negative-component vectors and are returned in
// closest-first order.
//
// RLS-scoped: this function does NOT add a tenant_id filter — the
// surrounding WithTenant tx already constrains visible rows. That
// matters because the HNSW index is global; the planner applies the
// RLS predicate as a filter on the returned rows, which is fine at
// v1 scale (ARCHITECTURE.md §8 migration triggers spell out when this
// stops being fine).
func SearchKNN(
	ctx context.Context,
	tx pgx.Tx,
	queryVec []float32,
	k int,
) ([]ANNResult, error) {
	if len(queryVec) != EmbeddingDim {
		return nil, fmt.Errorf("repo.session_embeddings.search: vector dim %d != %d", len(queryVec), EmbeddingDim)
	}
	if k <= 0 {
		k = 10
	}

	rows, err := tx.Query(ctx, `
		SELECT session_id, embedding <=> $1 AS distance
		  FROM session_embeddings
		 ORDER BY embedding <=> $1
		 LIMIT $2
	`, pgvector.NewVector(queryVec), k)
	if err != nil {
		return nil, fmt.Errorf("repo.session_embeddings.search: %w", err)
	}
	defer rows.Close()

	var out []ANNResult
	for rows.Next() {
		var r ANNResult
		var distance float64
		if err := rows.Scan(&r.SessionID, &distance); err != nil {
			return nil, fmt.Errorf("repo.session_embeddings.search scan: %w", err)
		}
		r.Similarity = 1 - distance
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.session_embeddings.search iter: %w", err)
	}
	return out, nil
}

// vecScanner is a sql.Scanner shim that lands the pgvector text/binary
// representation into a destination []float32 — we don't want to leak
// pgvector.Vector into the storage struct because callers shouldn't
// have to import the binding to read a row.
type vecScanner struct{ dst *[]float32 }

func (v vecScanner) Scan(src any) error {
	var pv pgvector.Vector
	if err := pv.Scan(src); err != nil {
		return err
	}
	*v.dst = pv.Slice()
	return nil
}

// scanVecInto returns a sql.Scanner that decodes the embedding column
// into dst. Using this helper keeps the SELECT call sites symmetric:
// every other column is `&out.Field`, this one is `scanVecInto(&out.Vec)`.
func scanVecInto(dst *[]float32) any {
	return vecScanner{dst: dst}
}
