# Iter — Deploy

## Hosting targets

| Component | Host | Notes |
|---|---|---|
| Go server (gateway + API + workers + cron) | Railway | Single binary, three environments: dev, staging, production. |
| Postgres 16+ (Railway image currently ships PG 18.4) + pgvector | Railway managed | Verify pgvector + citext + pgcrypto extensions enabled. |
| Redis | Railway managed | Both cache and Redis Streams durable queue. |
| Nightly scoring batch | Modal | Scheduled function; warm pool N=2. |
| Object archive | Cloudflare R2 | `iter-archive-prod` bucket; versioning enabled; lifecycle to Infrequent Access at 1 year. S3-compatible API, zero egress fees. |
| Auth | WorkOS | Hosted; OIDC + device-code flow + SAML for enterprise. |
| LLM observability | Self-hosted Langfuse on Railway | Same Railway project; separate service. |
| Logs + metrics + uptime + status page + on-call | BetterStack | Single vendor. |
| Domain | iter.dev | Apex pointed at Railway; subdomains: staging.iter.dev, status.iter.dev. |

Gateway WS hosting decision (deferred to verification): Railway WebSocket support for production scale must be confirmed. If insufficient at ~3K+ concurrent connections, gateway moves to Fly.io; rest of stack stays on Railway. Documented as a phase-8 contingency.

### Railway environments

The `iter` Railway project uses three long-lived environments. Each environment has an isolated Postgres service, Redis service, and `iter-server` variable scope.

| Environment | Purpose | Postgres service | Redis service | App service state |
|---|---|---|---|---|
| `dev` | Local/dev integration against cloud-managed data services. Placeholder provider keys are allowed. | `Postgres-IVFh` | `Redis` | `iter-server` variables populated; no binary deployed yet. |
| `staging` | Main-branch verification target before production promotion. Production-grade secrets required before external smoke tests. | `Postgres-f-fd` | `Redis-B2wt` | `iter-server` manually deployed at `https://iter-server-staging.up.railway.app`; `/health` verified. |
| `production` | Manual promotion target for iter.dev. | `Postgres` | `Redis-6Z2f` | `iter-server` variables partially populated; no binary deployed yet. |

The production environment also contains older Postgres services from earlier provisioning attempts. The canonical production database for Iter v1 is the `Postgres` service unless a later decision log entry changes it.

## Environment variables (production)

Set in Railway env vars per environment. Doppler deferred per phase 7.

### Required for the Go binary

```
# Postgres
# Request-path: uses `iter_app` role (LOGIN NOSUPERUSER NOBYPASSRLS).
# RLS is enforced — the Go binary MUST `SET LOCAL app.current_tenant = '<uuid>'`
# at the start of every tenant-scoped transaction.
DATABASE_URL=postgres://iter_app:<pw>@postgres.railway.internal:5432/railway?sslmode=require
PGBOUNCER_URL=postgres://iter_app:<pw>@... (transaction-mode pooler; same user as DATABASE_URL)

# Batch path: uses `iter_batch` role (BYPASSRLS). ONLY for the Modal nightly
# scoring batch and the archive cron — NEVER reachable from the request path.
DATABASE_URL_BATCH=postgres://iter_batch:<pw>@postgres.railway.internal:5432/railway?sslmode=require

# Admin/migration path: the original Railway-auto-populated `postgres` superuser
# URL, preserved as `DATABASE_URL_SUPERUSER` so `goose up`, `psql`, and PITR
# tooling still have a way in. NEVER used by application code.
DATABASE_URL_SUPERUSER=postgres://postgres:<pw>@postgres.railway.internal:5432/railway?sslmode=require

# Redis
REDIS_URL=redis://...

# WorkOS
WORKOS_CLIENT_ID=...
WORKOS_API_KEY=...
WORKOS_REDIRECT_URI=https://iter.dev/auth/callback

# LLM providers (in priority order)
ANTHROPIC_API_KEY=...
OPENAI_API_KEY=...
GOOGLE_AI_API_KEY=...
TOGETHER_API_KEY=...   # for Qwen / open-weights

# Embedding provider
VOYAGE_API_KEY=...

# Cloudflare R2 (S3-compatible; reuse AWS SDK with custom endpoint)
R2_ACCOUNT_ID=...
R2_ACCESS_KEY_ID=...
R2_SECRET_ACCESS_KEY=...
R2_ENDPOINT=https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com
R2_ARCHIVE_BUCKET=iter-archive-prod
R2_REGION=auto                     # R2 is region-less; SDKs require a string

# R2 free-tier guardrails (see "R2 usage monitoring" below)
R2_FREE_STORAGE_GB=10
R2_FREE_CLASS_A_OPS=1000000        # writes, lists, multipart
R2_FREE_CLASS_B_OPS=10000000       # reads (GET/HEAD)
R2_USAGE_ALERT_THRESHOLD=0.80      # 80% of any free-tier metric
CLOUDFLARE_API_KEY=...           # read-only token, scoped to R2 + Analytics

# Modal
MODAL_TOKEN_ID=...
MODAL_TOKEN_SECRET=...

# Webhook signing secrets
GITHUB_WEBHOOK_SECRET=...
LINEAR_WEBHOOK_SECRET=...

# Observability
BETTERSTACK_SOURCE_TOKEN=...
# Langfuse self-hosted on Railway — per-env project keys. The Go binary
# emits one async generation event per LLM provider call via the
# /api/public/ingestion endpoint. All three vars must be set for tracing
# to enable; with any one unset, the binary logs
# "langfuse tracing disabled" at boot and continues without emission.
LANGFUSE_BASE_URL=https://langfuse-web-dev.up.railway.app   # scheme+host, no trailing slash
LANGFUSE_PUBLIC_KEY=pk-lf-...
LANGFUSE_SECRET_KEY=sk-lf-...                              # never log; never echo

# Runtime
APP_ENV=production
PORT=8080
LOG_LEVEL=info
```

### Healthcheck

```
GET /health
```

Returns:

```json
{
  "ok": true,
  "version": "0.4.2",
  "db": "ok",
  "redis": "ok",
  "llm_routes": {
    "anthropic": "ok",
    "openai": "ok",
    "google": "degraded"
  },
  "uptime_seconds": 3601
}
```

Returns 200 if `db` and `redis` are `ok`. Returns 503 otherwise. LLM provider status is informational; the binary stays up even if all providers are down (suggest will return `no_suggestion_reason: llm_unavailable`).

Railway and BetterStack both probe `/health` every 30s.

## Deploy command

### Staging

Until GitHub-source auto-deploy is wired in the Railway dashboard, staging is deployed manually from the repo root:

```bash
railway up --service iter-server --environment staging
```

The staging service uses the repo `Dockerfile`, which builds `cmd/server`, installs `goose`, and runs:

```bash
goose -dir /app/migrations postgres "$DATABASE_URL_SUPERUSER" up && exec server
```

Current generated staging URL: `https://iter-server-staging.up.railway.app`.

### Production (manual promotion)

```bash
# After staging has run for ≥1h with no new error spikes:
railway up --service iter-server --environment production
```

Or via Railway UI: Promote staging build to production.

Migrations run automatically on boot via `goose up`. If a migration fails, the binary exits non-zero and Railway holds the previous version live.

### Modal scoring batch (separate deploy)

```bash
modal deploy modal/scoring.py
```

Triggered separately when the scoring code changes. Verify warm pool size in the dashboard.

### Mac app (TestFlight + direct download)

```bash
# Build, sign, notarize, staple:
make mac-release

# Upload to TestFlight:
make mac-upload-testflight

# Cut a public release:
# (After TestFlight users have approved for ≥48h:)
make mac-publish
```

Public download from iter.dev/download.

## Rollback plan

### Server rollback

```bash
railway rollback --service iter-server --environment production
```

Or via Railway UI: Click a previous deployment, "Redeploy."

Rollback time: ~2 minutes. Database migrations are forward-only (no rollback migrations); rollback assumes the previous binary is compatible with the current schema. For schema-breaking changes, follow the expand/contract pattern:

1. Deploy code that writes both old and new schema.
2. Migrate data.
3. Deploy code that only writes new schema.
4. Drop old schema.

Each step is independently rollbackable.

### Modal job rollback

```bash
modal deploy --tag previous modal/scoring.py
```

### Mac app rollback

TestFlight: revoke the build. Public download: replace the .dmg on iter.dev/download with the previous version. Users on a bad version can downgrade by reinstalling.

### Data rollback

**Postgres PITR (deferred):** Railway's point-in-time-recovery is gated to the Pro plan. The v1 deployment runs on the free Hobby plan, which only retains daily snapshots managed by Railway. The PITR drill (issue `006`) is deferred to whenever revenue or enterprise pilots justify the Pro upgrade — track in `issues/deferred/006-pitr-backup-restore-drill.md`. Until then, the rollback story for Postgres is "restore the most recent Railway snapshot" (manual, accept up to ~24h loss). Re-enable the drill and re-evaluate the 1h recovery target stated in `ARCHITECTURE.md` §7 as part of the Pro-plan migration.

R2 archive: versioning enabled. Restore a previous version with `wrangler` or any S3-compatible client pointed at `$R2_ENDPOINT`:

```bash
# Wrangler (Cloudflare-native; preferred for one-off restores)
wrangler r2 object list iter-archive-prod --prefix <tenant_id>/
wrangler r2 object get iter-archive-prod/<key> --file restored.tar.zst

# Or AWS CLI with R2 endpoint (same call shape as the old S3 commands)
aws s3api --endpoint-url "$R2_ENDPOINT" list-object-versions --bucket iter-archive-prod --prefix <tenant_id>/
aws s3api --endpoint-url "$R2_ENDPOINT" copy-object --bucket iter-archive-prod --copy-source ... --version-id ...
```

## R2 usage monitoring

The Cloudflare R2 free plan covers **10 GB storage, 1M Class A ops/month, 10M Class B ops/month, and zero-fee egress**. Overage rates: $0.015/GB-month storage, $4.50/M Class A, $0.36/M Class B. The goal of this section is to stay inside the free allotment until usage forces a deliberate upgrade.

### Scale honesty

Per `DECISIONS.md` phase 2, projected steady-state archive ingest is ~95 GB/month / ~1.1 TB/year. The free tier comfortably covers the 30-engineer bootcamp cohort and dev/staging environments, but **production at v1 scale (≥500 engineers) will exceed 10 GB within days**. The monitoring below is what tells us *when* to upgrade — not a fiction that we'll stay free forever.

### Metric collection (every 15 min)

The archive cron's heartbeat handler queries Cloudflare's Analytics API and emits gauges to BetterStack:

| Gauge | Source | Free-tier ceiling |
|---|---|---|
| `r2.storage_gb` | `GET /accounts/{id}/r2/buckets/iter-archive-prod` (`payload_size`) | 10 GB |
| `r2.class_a_ops_mtd` | Analytics GraphQL `r2OperationsAdaptiveGroups` (PUT/POST/LIST/DELETE) | 1,000,000 |
| `r2.class_b_ops_mtd` | Analytics GraphQL `r2OperationsAdaptiveGroups` (GET/HEAD) | 10,000,000 |
| `r2.egress_gb_24h` | Analytics GraphQL `r2OperationsAdaptiveGroups` (`responseObjectSize`) | no hard cap; anomaly-watched only |

Counters are month-to-date (reset on the 1st UTC). Auth uses `CLOUDFLARE_API_KEY` scoped to **Account → R2 → Read** and **Account → Analytics → Read**.

### Alerts (BetterStack)

| Severity | Trigger | Notify |
|---|---|---|
| P1 | `r2.storage_gb / R2_FREE_STORAGE_GB ≥ 0.80` for two consecutive samples | Email founder |
| P1 | `r2.class_a_ops_mtd / R2_FREE_CLASS_A_OPS ≥ 0.80` | Email founder |
| P1 | `r2.class_b_ops_mtd / R2_FREE_CLASS_B_OPS ≥ 0.80` | Email founder |
| P2 | `r2.egress_gb_24h > 2× rolling 7-day baseline` (sudden spike — possible scraping or runaway client) | Email founder |
| P3 | Cloudflare Analytics API auth failure for ≥3 cycles (metrics blind) | Email founder |

The 80% trigger is intentionally aggressive — overage on Class A ops is the most expensive failure mode ($4.50/M), and 80% gives ~6 days of headroom at typical archive-cron write rates.

### Hard-stop guardrail (defense in depth)

Independent of the alerts, the archive cron itself reads the same gauges and **refuses to write new objects** when any metric is ≥95% of its free-tier ceiling. Failed writes raise an alert and re-enqueue the object for the next run. This guarantees we don't silently sail past the free tier between alert and human response. To deliberately exceed the free tier, set `R2_OVERAGE_OK=true` — the guardrail is bypassed and overage is billed normally.

### Where it lives in the binary

`internal/archive/r2_meter.go` — Cloudflare API client, gauge emitter, guardrail check. `internal/archive/cron.go` calls the guardrail before every `PutObject`. Runbook: `runbooks/r2-quota-exceeded.md`.

## GitHub branch protection (HITL, one-time)

CI workflows live in `.github/workflows/ci.yml` and run on every PR + push to `main`. The actual protection rules must be configured by a human in **Settings → Branches → Branch protection rules** because the GitHub API for them is org-admin scoped.

Required configuration for the `main` branch:

- [ ] Require a pull request before merging.
- [ ] Require **≥1 approval** on each PR.
- [ ] Require status checks to pass before merging. Required checks (names match the `name:` keys in `ci.yml`):
  - `ci / lint`
  - `ci / test`
  - `ci / build`
  - `ci / test-integration`
- [ ] Require branches to be up to date with `main` before merging.
- [ ] Require linear history (no merge commits; rebase or squash).
- [ ] Restrict force-pushes to `main`.
- [ ] Restrict deletions of `main`.

CodeQL (`.github/workflows/codeql.yml`) is **not** a required check — findings surface in the Security tab but are advisory at v1. Dependabot (`.github/dependabot.yml`) opens PRs weekly; those PRs go through the same required-check set as any human PR.

Renaming a CI job (the `name:` key) is a breaking change to branch protection and must be coordinated with re-selecting the new check name in the protection rule.

## First deploy checklist

| Check | dev | staging | production |
|---|---:|---:|---:|
| Railway environment exists. | [x] | [x] | [x] |
| Postgres service provisioned. | [x] | [x] | [x] |
| Redis service provisioned. | [x] | [x] | [x] |
| Postgres extensions verified: `pgvector`, `pgcrypto`, `citext`. | [x] | [x] | [x] |
| Migrations run through current goose version; schema verified with `\dt`. | [x] | [x] | [x] |
| `iter_batch` role exists with `BYPASSRLS`. | [x] | [x] | [x] |
| `iter_app` role exists with `NOSUPERUSER NOBYPASSRLS` + table grants. | [x] | [x] | [x] |
| `scripts/provision-app-role.sh` run: `DATABASE_URL` (iter_app), `DATABASE_URL_BATCH` (iter_batch), and `DATABASE_URL_SUPERUSER` (postgres) set in Railway. | [x] | [x] | [x] |
| `scripts/verify-rls-bypass.sh` passes against the live Railway DB. | [x] | [x] | [x] |
| `REDIS_URL` set on `iter-server`. | [x] | [x] | [x] |
| R2 free-tier guardrail vars set. | [x] | [x] | [x] |
| `iter-server` binary deployed. | [ ] | [x] | [ ] |
| Generated Railway domain verified. | [x] | [x] | [ ] |
| `/health` returns `200` with `"ok":true`. | [ ] | [x] | [ ] |
| WorkOS app configured with matching redirect URI. | [ ] | [ ] | [ ] |
| All LLM provider keys verified with a smoke call. | [ ] | [ ] | [ ] |
| R2 bucket created via `wrangler r2 bucket create iter-archive-prod`; versioning + lifecycle (Infrequent Access at 1y) applied. | [ ] | [ ] | [ ] |
| R2 API token issued (read-only for analytics; separate read/write token for the Go binary's archive role). | [ ] | [ ] | [ ] |
| Modal scoring function deployed; warm pool live. | [ ] | [ ] | [ ] |
| BetterStack monitors created for: /health, suggest P99, error rate, scoring batch, Postgres connections, WS connection count, trufflehog scan failure rate, **R2 storage (≥80% of 10 GB), R2 Class A ops (≥80% of 1M/mo), R2 Class B ops (≥80% of 10M/mo), R2 egress anomaly (>2× 7-day rolling baseline)**. | [ ] | [ ] | [ ] |
| BetterStack on-call configured: email to founder. | [ ] | [ ] | [ ] |
| status.iter.dev published. | n/a | n/a | [ ] |
| Langfuse self-hosted on Railway, accessible at langfuse.iter.dev. | [ ] | [ ] | [ ] |
| GitHub webhook configured for the iter repo (for outcome attachment when Iter dogfoods itself). | [ ] | [ ] | [ ] |
| Linear webhook configured. | [ ] | [ ] | [ ] |
| Domain DNS verified. | n/a | [ ] | [ ] |
| TLS cert provisioned (Railway handles). | n/a | [ ] | [ ] |
| Runbooks committed to repo. | [ ] | [ ] | [ ] |
| On-call founder has read every runbook. | n/a | n/a | [ ] |
