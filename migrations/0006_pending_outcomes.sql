-- Iter v1 — pending outcomes buffer for late-binding webhook events.
--
-- Webhook deliveries (GitHub PR merge / push / check_run; Linear issue
-- state changes) arrive without a session_id. The handler matches each
-- event to a session by (repo_hash, commit_sha) or by a "Closes session:
-- <uuid>" marker in commit messages. When no session matches — common in
-- the first hours after a session is created and CI is still queued, or
-- because the agent's commit landed before the daemon's session reached
-- the cloud — we MUST not drop the event. GitHub retries 5xx-only and
-- only briefly; missing the merge signal silently degrades the scoring
-- training set forever.
--
-- This table buffers the raw event. A future late-match sweeper joins
-- pending_outcomes to sessions by repo_hash and replays into the
-- outcomes table. 7d retention is a TODO that hits at scale; for v1 we
-- accept unbounded growth and document the cleanup gap in
-- DECISIONS.md.
--
-- NOT tenant-scoped: at receive time the matching tenant is unknown.
-- That's the whole point of buffering. iter_app reads/writes; iter_batch
-- reads during the nightly sweep. RLS NOT enabled — there's no
-- tenant_id column to filter by, and the read path is server-side
-- background work, never the request path.
--
-- References:
--   * ARCHITECTURE.md §9 Step 5 "Webhook edge cases"
--   * issues/041-github-webhook.md
--   * DECISIONS.md "Commit-message session marker (issue 041)"

-- +goose Up

CREATE TABLE pending_outcomes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source          text NOT NULL CHECK (source IN ('github','linear')),
  delivery_id     text NOT NULL,
  event_type      text NOT NULL,
  payload         jsonb NOT NULL,
  received_at     timestamptz NOT NULL DEFAULT now(),
  matched_at      timestamptz,
  UNIQUE (source, delivery_id)
);

-- Sweep index: the late-match job scans by received_at ASC to retry the
-- oldest unmatched events first.
CREATE INDEX idx_pending_outcomes_received
  ON pending_outcomes(received_at)
  WHERE matched_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE pending_outcomes TO iter_app;
GRANT SELECT, UPDATE, DELETE ON TABLE pending_outcomes TO iter_batch;

-- +goose Down

REVOKE ALL ON TABLE pending_outcomes FROM iter_batch;
REVOKE ALL ON TABLE pending_outcomes FROM iter_app;
DROP TABLE IF EXISTS pending_outcomes;
