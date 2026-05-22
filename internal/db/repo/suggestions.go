package repo

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// Suggestion mirrors the suggestions table column-for-column. The vector
// column uses pgvector.Vector (text-wire) which pgx accepts via
// driver.Valuer + sql.Scanner.
//
// SuggestionHash is sha256(tenant_id_bytes || source_prompt_bytes). See
// DECISIONS.md "Phase 3 — `suggestion_hash` formula" and migrations/
// 0003_suggestion_hash.sql for the rationale. The unique constraint backs
// the idempotent Upsert path.
type Suggestion struct {
	ID                 uuid.UUID       `db:"id"`
	TenantID           uuid.UUID       `db:"tenant_id"`
	SuggestionHash     []byte          `db:"suggestion_hash"`
	SourcePrompt       string          `db:"source_prompt"`
	SourceEmbedding    pgvector.Vector `db:"source_embedding"`
	RefinedPrompt      string          `db:"refined_prompt"`
	Rationale          *string         `db:"rationale"`
	EvidenceSessionIDs []uuid.UUID     `db:"evidence_session_ids"`
	HitCount           int32           `db:"hit_count"`
	AcceptCount        int32           `db:"accept_count"`
	CreatedAt          time.Time       `db:"created_at"`
	LastUsedAt         *time.Time      `db:"last_used_at"`
}

// SuggestionWithScore augments Suggestion with the cosine distance
// returned by an HNSW search. Score is `1 - cosine_distance`, i.e. closer
// to 1.0 means more similar. Returned by SearchKNN and TopByAcceptance
// (the latter sets Score = 0 — callers should ignore it there).
type SuggestionWithScore struct {
	Suggestion
	Score float64
}

// SuggestionHash returns the canonical dedup key for the (tenant_id,
// source_prompt) pair. The 16-byte tenant_id binary form is concatenated
// with the UTF-8 prompt bytes, then SHA-256'd. Recorded in DECISIONS.md
// so clients (Go API, future Python tooling) hash the same way.
func SuggestionHash(tenantID uuid.UUID, sourcePrompt string) []byte {
	h := sha256.New()
	tid, _ := tenantID.MarshalBinary() // 16 bytes; never errors for uuid.UUID
	h.Write(tid)
	h.Write([]byte(sourcePrompt))
	return h.Sum(nil)
}

// UpsertSuggestion inserts a new suggestion row keyed by
// (tenant_id, source_prompt) via suggestion_hash. On conflict the
// existing row's hit_count is incremented and last_used_at is bumped to
// now(); the rest of the row (refined_prompt, embedding, etc.) is left
// alone so concurrent callers can't trample a higher-quality refinement
// with a stale one.
//
// Returns the persisted row (with server-assigned id when new, or the
// pre-existing id on conflict).
func UpsertSuggestion(ctx context.Context, tx pgx.Tx, s Suggestion) (Suggestion, error) {
	if s.TenantID == uuid.Nil {
		return Suggestion{}, errors.New("repo.suggestions.upsert: tenant_id required")
	}
	if s.SourcePrompt == "" {
		return Suggestion{}, errors.New("repo.suggestions.upsert: source_prompt required")
	}
	if s.RefinedPrompt == "" {
		return Suggestion{}, errors.New("repo.suggestions.upsert: refined_prompt required")
	}
	if len(s.SourceEmbedding.Slice()) == 0 {
		return Suggestion{}, errors.New("repo.suggestions.upsert: source_embedding required")
	}
	// Always recompute the hash from the canonical inputs; never trust a
	// caller-supplied hash that disagrees with tenant_id||source_prompt.
	s.SuggestionHash = SuggestionHash(s.TenantID, s.SourcePrompt)
	if s.EvidenceSessionIDs == nil {
		s.EvidenceSessionIDs = []uuid.UUID{}
	}

	var out Suggestion
	err := tx.QueryRow(ctx, `
		INSERT INTO suggestions (
		  tenant_id, suggestion_hash, source_prompt, source_embedding,
		  refined_prompt, rationale, evidence_session_ids,
		  hit_count, accept_count, last_used_at
		) VALUES (
		  $1, $2, $3, $4, $5, $6, $7, 0, 0, now()
		)
		ON CONFLICT (suggestion_hash) DO UPDATE
		   SET hit_count = suggestions.hit_count + 1,
		       last_used_at = now()
		RETURNING
		  id, tenant_id, suggestion_hash, source_prompt, source_embedding,
		  refined_prompt, rationale, evidence_session_ids,
		  hit_count, accept_count, created_at, last_used_at
	`,
		s.TenantID, s.SuggestionHash, s.SourcePrompt, s.SourceEmbedding,
		s.RefinedPrompt, s.Rationale, s.EvidenceSessionIDs,
	).Scan(
		&out.ID, &out.TenantID, &out.SuggestionHash, &out.SourcePrompt,
		&out.SourceEmbedding, &out.RefinedPrompt, &out.Rationale,
		&out.EvidenceSessionIDs, &out.HitCount, &out.AcceptCount,
		&out.CreatedAt, &out.LastUsedAt,
	)
	if err != nil {
		return Suggestion{}, fmt.Errorf("repo.suggestions.upsert: %w", err)
	}
	return out, nil
}

// SearchKNN returns the top-k suggestions most similar to queryVec under
// cosine distance, using the HNSW index. RLS scopes the result set to
// the calling tenant. The cosine distance operator is `<=>`; the score
// reported back is `1 - distance` so callers can think in terms of
// similarity (higher = better).
//
// k <= 0 falls back to 5. The caller should keep k small — HNSW recall
// degrades past a few dozen.
func SearchKNN(ctx context.Context, tx pgx.Tx, queryVec []float32, k int) ([]SuggestionWithScore, error) {
	if k <= 0 {
		k = 5
	}
	if len(queryVec) == 0 {
		return nil, errors.New("repo.suggestions.search_knn: empty query vector")
	}
	q := pgvector.NewVector(queryVec)
	rows, err := tx.Query(ctx, `
		SELECT
		  id, tenant_id, suggestion_hash, source_prompt, source_embedding,
		  refined_prompt, rationale, evidence_session_ids,
		  hit_count, accept_count, created_at, last_used_at,
		  source_embedding <=> $1 AS distance
		  FROM suggestions
		 ORDER BY source_embedding <=> $1 ASC
		 LIMIT $2
	`, q, k)
	if err != nil {
		return nil, fmt.Errorf("repo.suggestions.search_knn: %w", err)
	}
	defer rows.Close()

	out := make([]SuggestionWithScore, 0, k)
	for rows.Next() {
		var s SuggestionWithScore
		var dist float64
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.SuggestionHash, &s.SourcePrompt,
			&s.SourceEmbedding, &s.RefinedPrompt, &s.Rationale,
			&s.EvidenceSessionIDs, &s.HitCount, &s.AcceptCount,
			&s.CreatedAt, &s.LastUsedAt, &dist,
		); err != nil {
			return nil, fmt.Errorf("repo.suggestions.search_knn scan: %w", err)
		}
		s.Score = 1.0 - dist
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.suggestions.search_knn iter: %w", err)
	}
	return out, nil
}

// IncrementHitCount bumps a single row's hit_count by 1 and refreshes
// last_used_at. Used by the request path when a SearchKNN result is
// served (the cache hit) without re-running the refinement LLM. Returns
// pgx.ErrNoRows when the id is missing or hidden by RLS.
func IncrementHitCount(ctx context.Context, tx pgx.Tx, suggID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE suggestions
		   SET hit_count = hit_count + 1,
		       last_used_at = now()
		 WHERE id = $1
	`, suggID)
	if err != nil {
		return fmt.Errorf("repo.suggestions.incr_hit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.suggestions.incr_hit: %w", pgx.ErrNoRows)
	}
	return nil
}

// IncrementAcceptCount bumps accept_count by 1. Called by the client
// (CLI / daemon) when it reports back that the user actually used the
// suggestion. Drives TopByAcceptance.
func IncrementAcceptCount(ctx context.Context, tx pgx.Tx, suggID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE suggestions
		   SET accept_count = accept_count + 1
		 WHERE id = $1
	`, suggID)
	if err != nil {
		return fmt.Errorf("repo.suggestions.incr_accept: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.suggestions.incr_accept: %w", pgx.ErrNoRows)
	}
	return nil
}

// TopByAcceptance returns up to `limit` suggestions ordered by
// accept_count DESC, restricted to rows whose last_used_at >= since.
// Drives the /v1/dashboard/team "top_patterns" panel. Score is left at
// zero — distance is meaningless in this context.
func TopByAcceptance(ctx context.Context, tx pgx.Tx, since time.Time, limit int) ([]SuggestionWithScore, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := tx.Query(ctx, `
		SELECT
		  id, tenant_id, suggestion_hash, source_prompt, source_embedding,
		  refined_prompt, rationale, evidence_session_ids,
		  hit_count, accept_count, created_at, last_used_at
		  FROM suggestions
		 WHERE last_used_at >= $1
		 ORDER BY accept_count DESC, last_used_at DESC NULLS LAST
		 LIMIT $2
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("repo.suggestions.top_by_acceptance: %w", err)
	}
	defer rows.Close()

	out := make([]SuggestionWithScore, 0, limit)
	for rows.Next() {
		var s SuggestionWithScore
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.SuggestionHash, &s.SourcePrompt,
			&s.SourceEmbedding, &s.RefinedPrompt, &s.Rationale,
			&s.EvidenceSessionIDs, &s.HitCount, &s.AcceptCount,
			&s.CreatedAt, &s.LastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("repo.suggestions.top_by_acceptance scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.suggestions.top_by_acceptance iter: %w", err)
	}
	return out, nil
}

// DeleteSuggestion removes a single row by id. Returns pgx.ErrNoRows
// when missing or RLS-hidden.
func DeleteSuggestion(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM suggestions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("repo.suggestions.delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.suggestions.delete: %w", pgx.ErrNoRows)
	}
	return nil
}
