-- Iter v1 — application role (request-path).
--
-- Companion to migration 0001:
--   * 0001 created `iter_batch` (BYPASSRLS) used by nightly scoring (Modal) and
--     the archive cron only — NEVER reachable from the request path.
--   * 0002 (this file) creates `iter_app` — the role the Go binary uses for all
--     request-path queries. RLS is enforced because `iter_app` has
--     NOSUPERUSER NOBYPASSRLS. Per CLAUDE.md "Locked invariants" /
--     ARCHITECTURE.md §3, every tenant-scoped query MUST be wrapped in a
--     transaction that runs `SET LOCAL app.current_tenant = '<uuid>'`.
--
-- The password is set out-of-band by `scripts/provision-app-role.sh`. Storing
-- it in the migration would commit a secret. See `deploy.md` for the runbook.
--
-- Grants:
--   * USAGE on schema public
--   * SELECT/INSERT/UPDATE/DELETE on all tenant-scoped tables + the lookup
--     tables (tenants, users, tenant_users) the app needs to authenticate
--     requests
--   * USAGE+SELECT on sequences (bigserial PKs on session_events, audit_log)
--
-- The app role does NOT receive grants on the `goose_db_version` table or on
-- pg_catalog. Migrations are run as the superuser (`postgres`) at deploy time.

-- +goose Up

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'iter_app') THEN
    CREATE ROLE iter_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION;
  ELSE
    -- Defensive: if the role exists from a prior provision, enforce the locked
    -- attributes. This is idempotent and safe to re-run.
    ALTER ROLE iter_app WITH LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION;
  END IF;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO iter_app;

-- Lookup tables (no RLS — auth/identity layer reads these directly).
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE tenants      TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE users        TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE tenant_users TO iter_app;

-- Tenant-scoped tables (RLS-protected; iter_app NOBYPASSRLS so the policies bite).
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE sessions           TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE session_events     TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE session_embeddings TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE session_scores     TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE outcomes           TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE suggestions        TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE stacks             TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE stack_shares       TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE archive_pointers   TO iter_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE audit_log          TO iter_app;

-- bigserial PKs require sequence USAGE for INSERTs.
GRANT USAGE, SELECT ON SEQUENCE session_events_id_seq TO iter_app;
GRANT USAGE, SELECT ON SEQUENCE audit_log_id_seq      TO iter_app;

-- iter_batch needs full access to every tenant-scoped table (it bypasses RLS
-- but still needs table-level GRANTs unless it owns the tables). It already
-- exists from migration 0001; we just attach the grants here so future
-- migrations don't have to remember.
GRANT USAGE ON SCHEMA public TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE tenants            TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE users              TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE tenant_users       TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE sessions           TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE session_events     TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE session_embeddings TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE session_scores     TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE outcomes           TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE suggestions        TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE stacks             TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE stack_shares       TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE archive_pointers   TO iter_batch;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE audit_log          TO iter_batch;
GRANT USAGE, SELECT ON SEQUENCE session_events_id_seq TO iter_batch;
GRANT USAGE, SELECT ON SEQUENCE audit_log_id_seq      TO iter_batch;

-- +goose Down

REVOKE ALL ON TABLE tenants, users, tenant_users,
                    sessions, session_events, session_embeddings, session_scores,
                    outcomes, suggestions, stacks, stack_shares,
                    archive_pointers, audit_log
  FROM iter_app, iter_batch;
REVOKE ALL ON SEQUENCE session_events_id_seq, audit_log_id_seq FROM iter_app, iter_batch;
REVOKE USAGE ON SCHEMA public FROM iter_app, iter_batch;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'iter_app') THEN
    DROP ROLE iter_app;
  END IF;
END
$$;
-- +goose StatementEnd
