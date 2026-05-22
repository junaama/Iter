-- Iter v1 — idempotency constraints for session_scores and outcomes.
--
-- Issue 052 introduces repository functions that need ON CONFLICT
-- semantics to make re-runs safe:
--
--   * session_scores: the nightly Modal scorer (ARCHITECTURE.md §9 Step 6)
--     may be invoked twice for the same (session_id, scorer_version) pair
--     after a retry. InsertScore wants `ON CONFLICT (session_id,
--     scorer_version) DO NOTHING`, which needs a unique constraint.
--
--   * outcomes: webhook handlers (issues 041/042) consume GitHub +
--     Linear events. The webhook receiver guarantees at-least-once
--     delivery, so two events with the same (session_id, outcome_type,
--     external_ref) must collapse to one row. external_ref is nullable
--     (e.g. "tests_passed" without a remote ref) so the unique index
--     is partial — it only applies where external_ref IS NOT NULL.
--     Rows without an external_ref are *not* deduped by the database
--     and must be deduped by the caller if needed.
--
-- Migration 0001 is immutable per CLAUDE.md "migrations/0001_initial.sql
-- is the initial schema". This file extends it.

-- +goose Up

-- session_scores: hard uniqueness on (session_id, scorer_version).
-- Re-running the scorer is a domain expectation; duplicates are not.
ALTER TABLE session_scores
  ADD CONSTRAINT uq_session_scores_session_version
  UNIQUE (session_id, scorer_version);

-- outcomes: partial uniqueness on (session_id, outcome_type, external_ref)
-- where external_ref is known. NULL external_ref bypasses dedup so
-- callers can intentionally record multiple "tests_failed" rows for a
-- single session without piggy-backing on a synthetic identifier.
CREATE UNIQUE INDEX uq_outcomes_session_type_ref
  ON outcomes (session_id, outcome_type, external_ref)
  WHERE external_ref IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS uq_outcomes_session_type_ref;
ALTER TABLE session_scores
  DROP CONSTRAINT IF EXISTS uq_session_scores_session_version;
