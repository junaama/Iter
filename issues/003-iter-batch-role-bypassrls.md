---
type: AFK
depends-on:
  - 002-migrations-directory-initial-schema
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 1 — Data model. See also `CLAUDE.md` "Locked invariants" — the `iter_batch` role has `BYPASSRLS` and must never be reachable from the request path.

## What to build

Create the privileged `iter_batch` Postgres role with `BYPASSRLS`, used by nightly scoring (Modal) and the archive cron only. The application role used by the request-path Go binary must NOT have `BYPASSRLS` — that's the whole point of RLS. Document which connection string each workload uses.

Add this as `migrations/0002_iter_batch_role.sql` (or fold into `0001` if the migration has not yet been applied to any environment outside dev — but per the immutable-migrations invariant, prefer a new migration).

## Acceptance criteria

- [ ] `iter_batch` role exists with `BYPASSRLS` attribute
- [ ] Application role (the one the Go binary will use) does NOT have `BYPASSRLS`
- [ ] `SELECT rolname, rolbypassrls FROM pg_roles WHERE rolname IN ('iter_batch', '<app_role>');` shows the correct distribution
- [ ] Both roles' credentials stored as separate Railway env vars (`DATABASE_URL` for app, `DATABASE_URL_BATCH` or equivalent for batch)
- [ ] Migration file checked in (immutable once applied beyond dev)
- [ ] `deploy.md` updated with both env var names and which workloads use which
- [ ] A short integration test or psql session demonstrates that the app role respects RLS while `iter_batch` bypasses it (using a sample row from issue 004 once available — or a throwaway row here)

## Blocked by

- Blocked by `issues/002-migrations-directory-initial-schema.md`

## User stories addressed

Foundational invariant; protects tenant isolation guarantees that every user story depends on.
