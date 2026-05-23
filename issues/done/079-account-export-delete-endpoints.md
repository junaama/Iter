---
type: AFK
depends-on:
  - 077-settings
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 8 (Settings data export and account deletion) and §7 row "User account deleted" (cascade-delete with confirmation).

## What to build

Implement the cloud account lifecycle endpoints required by the Mac Settings screen:

- `POST /v1/account/export` starts a tenant-scoped export for the signed-in user.
- Export status/polling endpoint returns pending/ready/failed plus a download URL or archive pointer when ready.
- `POST /v1/account/delete` soft-disables the signed-in user and schedules the documented 7-day cascade-delete path.
- Both POST endpoints require `Idempotency-Key`, authenticated tenant context, audit logging, and cross-tenant isolation.

## Acceptance criteria

- [ ] Mac Settings can start an export and poll until ready without showing the unavailable state
- [ ] Delete account request is accepted only for the authenticated user after confirmation in the client
- [ ] Audit log records export and delete requests without raw payloads or secrets
- [ ] Tests cover idempotency, auth failure, tenant mismatch, and missing `Idempotency-Key`
