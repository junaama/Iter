---
type: AFK
depends-on:
  - 001-provision-postgres-railway
---

# AFK — Railway CLI provisioning

This issue is CLI-ready now that `railway` is authenticated in the terminal. Workers may use the Railway CLI to create/link environments and services, set non-secret or already-provided variables, run migrations, and verify RLS. Do not print secret values. If a required production secret is missing from the local/Railway environment, use the documented placeholder only where the issue permits it; otherwise return the issue with a blocker note instead of inventing a value.

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Railway project (dev/staging/prod); Postgres + Redis provisioned; secrets in Railway env vars"). Issue 001 provisioned the production Postgres only; this slice creates the staging/prod environment scoping called for in §3 Compute layer and `deploy.md` "Hosting targets".

## What to build

Three Railway environments — `dev`, `staging`, `production` — each with Postgres + Redis provisioned and the full env-var set populated.

Specifically:

1. **Environment scoping**: per Railway's environment model, create `staging` and `production` alongside the existing `production` env from 001. Reconcile naming: if 001 named the env "production" and there is no dev/staging, rename it or add the others. Document the resolved naming in `deploy.md`.
2. **Postgres per env**: each environment gets its own Postgres service (with pgvector, pgcrypto, citext). Run `migrations/0001_initial.sql` against each. Verify with `\dt`.
3. **Redis per env**: each environment gets a Redis service. Capture `REDIS_URL` per env.
4. **Secrets per env**: populate the full env-var set from `deploy.md` "Environment variables (production)" per environment. Use placeholder/dev keys for dev; use existing authenticated/account-backed values for staging/prod when available. R2 guardrail vars (`R2_FREE_*`, `R2_USAGE_ALERT_THRESHOLD`) per env. Do not log secret values.
5. **Service health**: `staging` and `production` services are paused (no binary running yet — slice 060 deploys); just the data plane needs to exist.
6. **`scripts/provision-app-role.sh`** run against each env per `deploy.md` "First production deploy checklist" — `iter_app` + `iter_batch` + `iter_superuser` URLs minted.
7. **`scripts/verify-rls-bypass.sh`** passes against each env.
8. **`deploy.md` updated**: Hosting-targets table notes the three-env split; "First production deploy checklist" is split into dev/staging/prod columns.

## Acceptance criteria

- [ ] Three Railway environments: `dev`, `staging`, `production`
- [ ] Each env has Postgres (pgvector/pgcrypto/citext verified) and Redis services
- [ ] `migrations/0001_initial.sql` applied per env; `\dt` confirms schema
- [ ] `DATABASE_URL` / `DATABASE_URL_BATCH` / `DATABASE_URL_SUPERUSER` / `REDIS_URL` set per env
- [ ] `scripts/provision-app-role.sh` run + `scripts/verify-rls-bypass.sh` passes per env
- [ ] Full secret set from `deploy.md` populated per env (placeholders OK for dev; production-grade keys for staging/prod)
- [ ] `deploy.md` updated to reflect the env split
- [ ] `DECISIONS.md` updated if any naming/scope decision changed since 001

## Blocked by

- Blocked by `issues/done/001-provision-postgres-railway.md`

## User stories addressed

Foundation for 060 (Railway CD), 061 (DNS), 062 (BetterStack monitor). Required for the §9 Step 6 e2e tests that hit staging.
