---
type: HITL
depends-on:
  - 058-railway-staging-prod-environments
  - 059-github-actions-ci
---

# HITL remainder — Railway dashboard auto-deploy and production promotion

CD wiring requires Railway dashboard work + linking to GitHub. AFK workers should skip this issue.

The AFK staging deploy subset has been completed. The remaining work is the dashboard/GitHub auto-deploy and production promotion configuration.

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Railway CD: auto-deploy main → staging, manual promotion → prod"); `deploy.md` "Deploy command" — staging auto-deploys from `main`, production is a manual promotion via `railway up` or the dashboard's "Promote staging build to production" action.

## What to build

CD wiring such that every merge to `main` deploys the Go binary to Railway staging, and promotion to production is a deliberate manual action.

Specifically:

1. **Service in each environment**: create the `iter-server` Railway service in `staging` and `production` (env scoping from 058). Point each at the same GitHub repo.
2. **Staging auto-deploy**: configure `staging.iter-server` to auto-build from the `main` branch. Build command: `go build -ldflags "-X main.version=$(git describe --tags --dirty)" ./cmd/server`. Start command: `./server`.
3. **Production manual promotion**: configure `production.iter-server` to NOT auto-deploy. Promotion is via `railway up --service iter-server --environment production` or the dashboard's promote action (per `deploy.md`).
4. **Migrations on boot**: per `deploy.md` "Deploy command", `goose up` runs as part of the start command (`goose -dir migrations postgres "$DATABASE_URL_SUPERUSER" up && ./server`). If a migration fails, the binary exits non-zero and Railway holds the previous version live. Validate that behavior with a deliberately-broken migration in a throwaway branch.
5. **PORT binding**: Railway injects `PORT`; `cmd/server` from 048 already reads it. Sanity check: `curl https://<staging-domain>.up.railway.app/health` returns 200 after the first deploy.
6. **Build cache**: enable Railway's build cache so repeat deploys are fast.
7. **Deployment notifications**: connect Railway to BetterStack (the integration lands in 027) or to email-only at v1 (per `ARCHITECTURE.md` §7 "Email notifications only at v1").
8. **Rollback drill** (per `deploy.md` "Rollback plan"): deliberately deploy a known-bad build to staging, `railway rollback`, confirm staging is back to the previous version within 2 minutes. Document the run in the PR.

## Acceptance criteria

- [ ] `iter-server` Railway services exist in `staging` and `production`
- [ ] Staging auto-deploys on push to `main`
- [ ] Production does NOT auto-deploy
- [ ] `goose up` runs on boot; broken migration holds the previous version
- [ ] Staging `/health` returns 200 after first deploy (curl from any network)
- [ ] Build cache enabled
- [ ] Rollback drill: known-bad build → `railway rollback` → previous version live in ≤2 minutes; documented in PR
- [ ] `deploy.md` updated with the resolved service names + commands

## AFK staging deploy completed — 2026-05-23

- Added repo-level `Dockerfile` and `.dockerignore` for Railway builds.
- Docker image builds `cmd/server`, installs `goose` v3.27.1, copies `migrations/`, and starts with `goose -dir /app/migrations postgres "$DATABASE_URL_SUPERUSER" up && exec server`.
- Local Docker build passed: `docker build -t iter-server:060 .`.
- Set a generated staging-only `ITER_JWT_SECRET` in Railway without printing the value.
- Manual staging deploy succeeded: deployment `f78b16b9-86e7-497c-917b-4e050dc929fc`.
- Generated staging domain: `https://iter-server-staging.up.railway.app`.
- Verified staging health:

```json
{"ok":true,"version":"dev","db":"ok","redis":"ok","llm_routes":{},"embed_routes":{},"uptime_seconds":49}
```

Remaining HITL work:

- Configure GitHub-source auto-deploy from `main` to staging in the Railway dashboard.
- Confirm production does not auto-deploy and document the promotion path.
- Enable/confirm Railway build cache if it is dashboard-only.
- Run the deliberate staging rollback drill and document the result.
- Finish production-grade secret provisioning from issue 058 before external smoke tests.

## Blocked by

- Blocked by `issues/058-railway-staging-prod-environments.md`
- Blocked by `issues/059-github-actions-ci.md`

## User stories addressed

Every code-changing engineer relies on CD to land the binary on staging without ceremony. Production promotion is the §7 "incident response" guardrail — manual gate before user-facing changes.
