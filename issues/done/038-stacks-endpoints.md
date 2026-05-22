---
type: AFK
depends-on:
  - 034-tenant-context-middleware
  - 033-idempotency-middleware
  - 049-postgres-connection-layer
---

## Parent PRD

`ARCHITECTURE.md` §5 "REST endpoints" + §6 "Stack profile" + `CLAUDE.md` "Locked invariants" — **Stacks capture wrapped solutions … NEVER raw configs, env values, secrets, or MCP credentials.** Powers the Stack screens.

## What to build

Four endpoints, all behind auth + tenant-context (POSTs also behind idempotency).

### `GET /v1/stack/me`
Authenticated user's own stack rows for the current tenant.

### `GET /v1/stack/:user_id`
Another user's shared stacks. Returns only stacks where a `stack_shares` row exists granting the requesting user access. If the user_id is not in the same tenant: 404.

### `POST /v1/stack`
Create a new stack. Body validates against `Stack` wire type (`contracts.py`). The handler MUST:
1. Re-run trufflehog classification on the entire payload (`internal/redact.Classify` from 010). Reject if `dirty`; accept `clean` and successfully-redacted `strippable`.
2. Reject any field that looks like a raw config / env / secret per the invariant. Heuristic deny-list (in addition to trufflehog): any string matching `[A-Z][A-Z0-9_]{4,}=` shape (env-var-ish). Document in DECISIONS.md.
3. Persist the redacted payload.

### `POST /v1/stack/:id/share`
Body: `{ "shared_with_user_id": "uuid" }`. Both users must belong to the same tenant. Idempotent (the row's PK is `(stack_id, shared_with_user_id)`). Logs `stack_shared` to `audit_log`.

### `DELETE /v1/stack/:id/share/:user_id`
Revoke a share. Logs `stack_unshared`.

## Acceptance criteria

- [x] All 5 endpoints registered with correct middleware chain (POSTs include idempotency)
- [x] Classification path verified: `dirty` body → 422 with `{"error":"classification_failed","tier":"dirty"}`
- [x] Env-var heuristic catches a string like `OPENAI_API_KEY=sk-...` even when trufflehog misses it (test fixture)
- [x] Cross-tenant share returns 422 with `{"error":"cross_tenant_share_forbidden"}`
- [x] Cross-tenant `GET /v1/stack/:user_id` returns 404 (don't leak existence)
- [x] Audit-log entries written for share / unshare (verify in test against the `audit_log` table)
- [x] Wire types added to `pkg/contracts/` mirroring `contracts.py`
- [x] Integration tests cover happy paths + every failure mode
- [x] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/034-tenant-context-middleware.md`, `033-idempotency-middleware.md`
- Blocked by Step 3 storage-layer baseline — stacks + stack_shares + audit_log repository functions
- Soft-depends on `010-classification-trufflehog-wrapper-tests` (already done)

## User stories addressed

Stack share story; admin-only-discoverable team learnings.
