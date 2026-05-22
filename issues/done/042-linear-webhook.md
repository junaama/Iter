---
type: AFK
depends-on:
  - 041-github-webhook
---

## Parent PRD

`ARCHITECTURE.md` §5 + §7 (webhook patterns) + `CLAUDE.md` "Locked invariants" (HMAC + Idempotency on webhooks). Same architecture as 026 — reuse the verifier middleware.

## What to build

`POST /v1/webhooks/linear` — receives Linear `Issue` and `Comment` events. Maps `incident_*` and `linked-session-id` events to `outcomes` rows of type `incident_caused` and to `audit_log` `incident_linked` entries.

Mirrors 026's structure but:

- HMAC header is `Linear-Signature` (HMAC-SHA256 over the raw body, hex-encoded). Secret is `LINEAR_WEBHOOK_SECRET`.
- Idempotency key: Linear sends `Linear-Delivery` (UUID) — use that.
- Event routing:
  - `Issue` created with a `label` containing `incident` AND a `description` mentioning a session_id marker → `outcomes(outcome_type='incident_caused', external_ref=<issue_url>)`. Marker format same as GitHub commits.
  - `Comment` on an Issue: ignored at v1 (record decision in DECISIONS.md; revisit when ops asks for it).
  - `Issue` updated to status `Done` after an `incident_caused` outcome → audit-log `incident_resolved`. (No outcomes row update; the original `incident_caused` row stays as the historical signal.)

Refactor the HMAC + idempotency + event-routing skeleton into `internal/api/webhook/` so both 026 and this issue share the same verifier and event-router patterns. Document the shared shape in DECISIONS.md.

## Acceptance criteria

- [ ] Endpoint registered; HMAC verification via the shared webhook middleware
- [ ] Idempotency keyed by `Linear-Delivery`
- [ ] Event routing matches the table above
- [ ] Refactor of 026 + 027 into `internal/api/webhook/` complete (no duplicated HMAC code)
- [ ] Comment events skipped with a debug log line; not 400 (Linear retries unsigned 4xx)
- [ ] Integration tests with fixture payloads
- [ ] Shared webhook shape recorded in `DECISIONS.md`
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/041-github-webhook.md`

## User stories addressed

Linear incidents auto-flag the originating session as `incident_caused`. Powers the team-lead's "which sessions are correlated with incidents" view.
