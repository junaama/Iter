-- Iter v1 — scoring batch run ledger.
--
-- Companion to issue 046 (Modal nightly scoring batch). The Modal
-- function pulls "sessions with new events since the last successful
-- run" from this table; on success it writes a row with the max
-- session_events.occurred_at it consumed; on failure it leaves a
-- 'failed' row so the next-day run picks up everything since the most
-- recent SUCCEEDED row.
--
-- This is a global ops table, accessed only by the iter_batch
-- BYPASSRLS role. No tenant_id column and no RLS — by design.
-- The audit_log table is RLS-scoped and its event_type enum is closed
-- (see migrations/0001_initial.sql), so a per-tenant scoring failure
-- audit event would not fit there without expanding both. Instead the
-- Modal worker writes its own structured row here and the dashboard /
-- ops query joins this table when investigating failures.
--
-- References:
--   * ARCHITECTURE.md §9 Step 4 ("Modal scoring batch at 02:00 UTC")
--   * DECISIONS.md "Scoring batch runs table" (issue 046)

-- +goose Up

CREATE TABLE scoring_batch_runs (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  started_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz,
  scorer_version  text NOT NULL,
  status          text NOT NULL CHECK (status IN ('running','succeeded','failed')),
  sessions_scored integer NOT NULL DEFAULT 0,
  errors_count    integer NOT NULL DEFAULT 0,
  -- max(session_events.occurred_at) consumed by this run. The next run
  -- selects sessions with events > this watermark.
  last_event_at   timestamptz,
  details         jsonb NOT NULL DEFAULT '{}'
);

-- Hot path: "what's the most recent succeeded run?" — used at the top
-- of every Modal invocation to derive the new-events watermark.
CREATE INDEX idx_scoring_batch_runs_finished
  ON scoring_batch_runs(finished_at DESC)
  WHERE status = 'succeeded';

-- iter_batch is the only role that touches this table. iter_app is
-- explicitly NOT granted — the request path has no business reading or
-- writing batch-run state.
GRANT SELECT, INSERT, UPDATE ON TABLE scoring_batch_runs TO iter_batch;

-- +goose Down

REVOKE ALL ON TABLE scoring_batch_runs FROM iter_batch;
DROP TABLE IF EXISTS scoring_batch_runs;
