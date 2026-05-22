-- Iter v1 — tenant settings for dashboard-controlled team invites.
--
-- The dashboard/team response needs a persisted switch for whether admins
-- can show an invite affordance. Keep it on tenants as a small JSON object
-- until broader tenant-admin settings justify their own table.

-- +goose Up

ALTER TABLE tenants
  ADD COLUMN tenant_settings jsonb NOT NULL
  DEFAULT '{"team_invites_enabled": true, "invite_link_template": "https://iter.dev/invite?tenant_id={tenant_id}"}'::jsonb;

ALTER TABLE tenants
  ADD CONSTRAINT tenants_settings_is_object
  CHECK (jsonb_typeof(tenant_settings) = 'object');

-- +goose Down

ALTER TABLE tenants
  DROP CONSTRAINT IF EXISTS tenants_settings_is_object;

ALTER TABLE tenants
  DROP COLUMN IF EXISTS tenant_settings;
