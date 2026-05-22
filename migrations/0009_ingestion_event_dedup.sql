-- Iter v1 — ingestion replay deduplication.
--
-- The WebSocket gateway writes daemon trace events to Redis before the
-- server-side consumer persists them. Redis Streams are at-least-once, so
-- replayed deliveries need a stable event id in Postgres. The daemon's
-- wire msg_id is a UUID and becomes session_events.event_id.

-- +goose Up

ALTER TABLE session_events
  ADD COLUMN event_id uuid;

CREATE UNIQUE INDEX uq_session_events_tenant_session_event
  ON session_events(tenant_id, session_id, event_id)
  WHERE event_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS uq_session_events_tenant_session_event;

ALTER TABLE session_events
  DROP COLUMN IF EXISTS event_id;
