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

- [ ] Postgres 16+ instance running on Railway (at minimum: dev scope; staging/prod can follow in Step 3)
- [ ] `CREATE EXTENSION IF NOT EXISTS pgvector;` succeeds
- [ ] `CREATE EXTENSION IF NOT EXISTS pgcrypto;` succeeds
- [ ] `CREATE EXTENSION IF NOT EXISTS citext;` succeeds
- [ ] `pg_extension` query confirms all three are installed
- [ ] Postgres version verified as 16.x via `SELECT version();`
- [ ] Connection string stored as a Railway env var (not committed to repo)
- [ ] `deploy.md` updated if any provisioning step differs from what is currently documented

## Blocked by

None — can start immediately.

## User stories addressed

Foundational; not tied to a specific user story. Enables every other Step 1 slice and all of §3 Data layer.
