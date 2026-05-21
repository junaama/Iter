## Parent PRD

`ARCHITECTURE.md` §9 Step 1 — Data model. See also `CLAUDE.md` "Working with `schema.sql`" for the invariant that shipped migrations are immutable.

## What to build

Create a `migrations/` directory at the repo root and move the contents of `schema.sql` into it as `0001_initial.sql`. Pick and document a migration runner (e.g. `golang-migrate`, `goose`, `tern`) consistent with the Go-binary direction in §4. Apply `0001_initial.sql` against the Railway Postgres instance and confirm every table, type, index, and policy from `schema.sql` lands.

After this slice, `schema.sql` either (a) stays as a developer-readable snapshot pointing at `migrations/0001_initial.sql`, or (b) is removed in favor of the migration file — pick one and update `CLAUDE.md` to match.

## Acceptance criteria

- [ ] `migrations/0001_initial.sql` exists and matches `schema.sql` semantically
- [ ] Migration runner chosen and added to repo tooling (Makefile target or script, even if minimal)
- [ ] Running the migration against a fresh dev Postgres produces zero errors
- [ ] All tables from `schema.sql` exist (`\dt` shows the full set)
- [ ] All HNSW indexes exist with `m=16, ef_construction=64` as specified
- [ ] All RLS policies present and `ENABLE ROW LEVEL SECURITY` set on every tenant-scoped table
- [ ] Re-running the migration is a no-op or errors cleanly (idempotency at the runner level)
- [ ] `CLAUDE.md` "Working with `schema.sql`" updated to reference the new location
- [ ] Decision recorded in `DECISIONS.md` (migration runner choice)

## Blocked by

- Blocked by `issues/001-provision-postgres-railway.md`

## User stories addressed

Foundational; enables all RLS-, cascade-, and HNSW-dependent slices.
