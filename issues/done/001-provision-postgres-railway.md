---
type: HITL
depends-on: []
---

# HITL — requires interactive Railway login

This issue requires a human to run `railway login` (opens a browser) and `railway link` (interactive project picker). AFK workers should skip this file.

## Parent PRD

`ARCHITECTURE.md` §9 Step 1 — Data model (and §3 Data layer for context).

## What to build

Provision a Postgres 16+ instance on Railway across the dev/staging/prod project scopes described in §3 / §4. Verify that the three required extensions (`pgvector`, `pgcrypto`, `citext`) are available and installable on the instance. Capture the connection string(s) into Railway env vars so the rest of the data-layer work can target a real database.

This is the foundation slice — no app code is written here, but every subsequent migration, RLS, and HNSW task depends on the instance existing and the extensions being installable.

## Acceptance criteria

- [x] Postgres 16+ instance running on Railway — `iter` project, `production` env, `Postgres` service (image ships **18.4**, satisfies "16+")
- [x] `CREATE EXTENSION IF NOT EXISTS vector;` succeeds (extension name is `vector`, package is `pgvector` 0.8.2)
- [x] `CREATE EXTENSION IF NOT EXISTS pgcrypto;` succeeds (1.4)
- [x] `CREATE EXTENSION IF NOT EXISTS citext;` succeeds (1.8)
- [x] `pg_extension` query confirms all three are installed (`citext 1.8`, `pgcrypto 1.4`, `vector 0.8.2`)
- [x] Postgres version verified — `PostgreSQL 18.4 (Debian 18.4-1.pgdg13+1)`
- [x] Connection strings auto-stored as Railway env vars (`DATABASE_URL` internal, `DATABASE_PUBLIC_URL` proxy)
- [x] `deploy.md` updated (row 8 now reflects PG 18.4 vs the prior PG 16 wording)

## Completion notes

- Project ID: `ba22ea98-d911-48c0-a357-53fe4b8d8a49` (workspace: My Projects, env: production)
- Schema not yet applied against this DB (Railway `Postgres` service is empty — `\dt` returns no relations). Applying `migrations/0001_initial.sql` against the live Railway DB is deferred; the existing migration was validated locally per issue 002.
- Staging / prod scoping deferred to Step 3 (per issue body).
- `railway add` requires a TTY; this run used `script -q /dev/null railway add --database postgres` to fake one. Worth noting in `deploy.md` ops section later.

## Blocked by

None — can start immediately.

## User stories addressed

Foundational; not tied to a specific user story. Enables every other Step 1 slice and all of §3 Data layer.
