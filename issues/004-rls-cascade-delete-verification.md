## Parent PRD

`ARCHITECTURE.md` §9 Step 1 — Data model; §3 "Tenant isolation"; §7 "post-ingestion-leak" failure mode. See also `CLAUDE.md` "Working with `schema.sql`" — deleting a `session_id` must cascade to events, embeddings, scores, and outcomes.

## What to build

A verification script (Go test under `testcontainers` or a SQL script invoked from a Makefile target) that:

1. Inserts at least two tenants and sample rows for every tenant-scoped table.
2. Connects as the app role with `SET LOCAL app.current_tenant = '<tenant_a>'` and asserts that queries return only tenant_a's rows for every tenant-scoped table.
3. Repeats with `<tenant_b>` to confirm symmetric isolation.
4. Deletes a `sessions` row and asserts that `session_events`, `session_embeddings`, `scores`, and `outcomes` rows for that session are gone (cascade).
5. Deletes a `tenants` row and asserts the full cascade chain fires (every tenant-scoped table emptied for that tenant).

This becomes a permanent regression test — re-runnable as `make test-rls` or equivalent.

## Acceptance criteria

- [ ] Test exists and runs against a testcontainers Postgres (per §9 Step 6 "testcontainers, not mocks")
- [ ] All tenant-scoped tables enumerated explicitly; adding a new one without a policy fails the test
- [ ] Cross-tenant SELECT returns zero rows for the wrong tenant on every table
- [ ] Session cascade verified for `session_events`, `session_embeddings`, `scores`, `outcomes`
- [ ] Tenant cascade verified end-to-end (delete tenant → all rows gone)
- [ ] Test failure messages identify which table broke the invariant
- [ ] CI target wired so this runs on every PR (CI itself lands in Step 3, but the make target should exist now)

## Blocked by

- Blocked by `issues/002-migrations-directory-initial-schema.md`
- Blocked by `issues/003-iter-batch-role-bypassrls.md`

## User stories addressed

Underpins every multi-tenant user story — Adam, the team lead, the admin. If this breaks, every other story is unsafe.
