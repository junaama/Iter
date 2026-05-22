-- Iter v1 — Linear incident webhook audit events.
--
-- issue 042 maps Linear incident linkage/resolution to audit_log so the
-- team lead can trace which session was tied to an incident and when that
-- incident reached Done in Linear. The historical outcomes row remains
-- incident_caused; resolution is audit-only.

-- +goose Up

ALTER TABLE audit_log DROP CONSTRAINT audit_log_event_type_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_event_type_check CHECK (event_type IN (
  'tenant_created','tenant_deleted',
  'user_created','user_deleted','user_left_team',
  'stack_shared','stack_unshared',
  'incident_linked','incident_resolved',
  'leak_detected_post_ingestion','session_cascade_deleted',
  'score_model_rollback',
  'permissions_revoked','permissions_granted',
  'admin_action','data_export_requested','data_deletion_requested'
));

-- +goose Down

ALTER TABLE audit_log DROP CONSTRAINT audit_log_event_type_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_event_type_check CHECK (event_type IN (
  'tenant_created','tenant_deleted',
  'user_created','user_deleted','user_left_team',
  'stack_shared','stack_unshared',
  'leak_detected_post_ingestion','session_cascade_deleted',
  'score_model_rollback',
  'permissions_revoked','permissions_granted',
  'admin_action','data_export_requested','data_deletion_requested'
));
