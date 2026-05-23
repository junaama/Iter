-- Iter v1 — WorkOS identity link.
--
-- The Mac app (and CLI, in a future slice) signs in via WorkOS device-code
-- and exchanges the WorkOS access token for an Iter-issued session JWT at
-- POST /v1/auth/session. The exchange handler upserts a row in `users`
-- keyed by the WorkOS user id (e.g. "user_01KS...") so repeat sign-ins
-- resolve to the same Iter UUID instead of minting a new identity on
-- every device authorization.
--
-- Nullable: existing rows (seed data, AuthKit cookie-flow users that
-- predate this column) will have NULL until they sign in via the
-- device-code path. UNIQUE so the upsert can use ON CONFLICT.

-- +goose Up

ALTER TABLE users
  ADD COLUMN workos_user_id text;

CREATE UNIQUE INDEX uq_users_workos_user_id
  ON users(workos_user_id)
  WHERE workos_user_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS uq_users_workos_user_id;

ALTER TABLE users
  DROP COLUMN IF EXISTS workos_user_id;
