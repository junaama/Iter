-- Iter v1 — suggestions dedup key.
--
-- Motivation: `suggestions` is the cache table for `POST /v1/suggest`. The
-- request path computes `source_embedding` then either (a) finds a near-match
-- via HNSW (`SearchKNN`) and uses its `refined_prompt`, or (b) calls the LLM
-- to refine and persists a new suggestion. Path (b) must be idempotent — two
-- concurrent requests for the same `(tenant_id, source_prompt)` must collapse
-- to one row with `hit_count` incremented, not two rows competing for the
-- HNSW slot.
--
-- The deterministic dedup key is `sha256(tenant_id || source_prompt)` — see
-- DECISIONS.md "Phase 3 — `suggestion_hash` formula". The hash is computed in
-- Go (internal/db/repo/suggestions.go: SuggestionHash) and passed in as a
-- 32-byte value; the DB stores it as bytea with a UNIQUE constraint that
-- backs `UpsertSuggestion`'s `ON CONFLICT (suggestion_hash)`.
--
-- Why not a generated column (`sha256(tenant_id::text || source_prompt)`)?
-- Two reasons:
--   * pgcrypto's `digest()` would need to be enabled; we already require
--     `pgcrypto` but a generated column locks the formula to whatever Postgres
--     gives us today, harder to evolve.
--   * Keeping the hash in Go means the same value is reused by Redis cache
--     keys and audit-log payloads without re-hashing on the DB side.

-- +goose Up

ALTER TABLE suggestions
  ADD COLUMN suggestion_hash bytea;

-- Backfill: existing rows (none expected at v1; this migration ships before
-- the first request hits prod) get a deterministic placeholder so the NOT
-- NULL constraint applies cleanly. The placeholder collides on no plausible
-- input because it embeds the row id, not the prompt.
UPDATE suggestions
   SET suggestion_hash = decode(replace(id::text, '-', ''), 'hex')
 WHERE suggestion_hash IS NULL;

ALTER TABLE suggestions
  ALTER COLUMN suggestion_hash SET NOT NULL,
  ADD CONSTRAINT suggestions_suggestion_hash_len CHECK (octet_length(suggestion_hash) = 32),
  ADD CONSTRAINT suggestions_suggestion_hash_uniq UNIQUE (suggestion_hash);

-- +goose Down

ALTER TABLE suggestions
  DROP CONSTRAINT IF EXISTS suggestions_suggestion_hash_uniq,
  DROP CONSTRAINT IF EXISTS suggestions_suggestion_hash_len,
  DROP COLUMN IF EXISTS suggestion_hash;
