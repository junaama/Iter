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

## Comment — 2026-05-21 (current state before claim)

The `iter_batch` role is **already created** in `migrations/0001_initial.sql` (lines ~289–297, inside a `DO $$ ... $$` block guarded by `IF NOT EXISTS`). The migration has been applied to the Railway production DB:

```
$ psql "$DATABASE_PUBLIC_URL" -c "SELECT rolname, rolbypassrls FROM pg_roles WHERE rolname='iter_batch';"
  rolname   | rolbypassrls
------------+--------------
 iter_batch | t
```

So the first acceptance-criterion checkbox (`iter_batch` exists with `BYPASSRLS`) is effectively already satisfied on production. **Do NOT add `migrations/0002_iter_batch_role.sql`** — that would be a no-op and clutter the migration history. The "Add this as `migrations/0002_...`" sentence in the body is obsolete.

What's still genuinely open for this issue:

1. **Dedicated app role without `BYPASSRLS`.** Currently the only role created is `iter_batch`. The `DATABASE_URL` connection string points at the default `postgres` superuser, which is wrong for the request path (superuser also bypasses RLS implicitly). Create a new role, e.g. `iter_app`, with `LOGIN NOSUPERUSER NOBYPASSRLS` plus the minimum table-level grants. This DOES need a new migration (e.g. `0002_app_role.sql`).
2. **Separate Railway env vars.** Add `DATABASE_URL` (rewritten to use `iter_app`) and `DATABASE_URL_BATCH` (using `iter_batch`). The current `DATABASE_URL` Railway auto-populates uses `postgres` — you'll need to mint new passwords for the new roles and store them as Railway env vars (use `railway variables --set KEY=VALUE` per `scripts/load-railway-env.sh`). Keep `postgres` available for admin-only access; don't delete it.
3. **RLS-bypass demo.** Set `app.current_tenant`, insert a row, then show: connection as `iter_app` returns it; cross-tenant `SET LOCAL` returns zero rows; connection as `iter_batch` returns the row regardless of `app.current_tenant`. This is the only piece that 003 actually authors; the rest is plumbing.
4. **`deploy.md`** — document which workload uses which URL.

Railway DB facts you'll need:

- Project: `iter` (id `ba22ea98-d911-48c0-a357-53fe4b8d8a49`)
- Service: `Postgres`, env `production`
- Public proxy host: `kodama.proxy.rlwy.net:31648` (used by `DATABASE_PUBLIC_URL`)
- Internal host: `postgres.railway.internal:5432` (used by `DATABASE_URL`, only resolvable from inside Railway)
- PG version: 18.4 — superuser-managed; `ALTER ROLE` requires connecting as `postgres`
