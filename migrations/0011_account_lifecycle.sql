-- Iter v1 - account export and deletion request tracking.
--
-- The request path records tenant-scoped account export jobs and delayed
-- account deletion schedules. Export bundle generation can be picked up by a
-- later worker from account_exports rows without making the Settings API lie
-- about having produced a downloadable artifact.

-- +goose Up

CREATE TABLE account_exports (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status          text NOT NULL CHECK (status IN ('pending','ready','failed')),
  archive_pointer text,
  download_url    text,
  error           text,
  requested_at    timestamptz NOT NULL DEFAULT now(),
  ready_at        timestamptz,
  failed_at       timestamptz,
  expires_at      timestamptz,
  CHECK (
    (status = 'pending' AND ready_at IS NULL AND failed_at IS NULL)
    OR (status = 'ready' AND ready_at IS NOT NULL AND failed_at IS NULL AND (archive_pointer IS NOT NULL OR download_url IS NOT NULL))
    OR (status = 'failed' AND failed_at IS NOT NULL)
  )
);

CREATE INDEX idx_account_exports_tenant_user_time
  ON account_exports(tenant_id, user_id, requested_at DESC);

CREATE TABLE account_deletions (
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  requested_at    timestamptz NOT NULL DEFAULT now(),
  scheduled_for   timestamptz NOT NULL,
  completed_at    timestamptz,
  PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX idx_account_deletions_due
  ON account_deletions(scheduled_for)
  WHERE completed_at IS NULL;

ALTER TABLE account_exports   ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_deletions ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON account_exports
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON account_deletions
  USING (tenant_id = current_setting('app.current_tenant')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE account_exports   TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE account_deletions TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE account_exports   TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE account_deletions TO iter_batch;

-- +goose Down

DROP TABLE IF EXISTS account_deletions;
DROP TABLE IF EXISTS account_exports;
