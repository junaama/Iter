---
type: HITL
depends-on:
  - 018-repositories-suggestions-stacks-archive
---

# HITL — Cloudflare R2 bucket + token provisioning

R2 setup requires a human at the Cloudflare dashboard: create the `iter-archive-prod` bucket, enable versioning, set the lifecycle rule (Infrequent Access at 1 year per `deploy.md`), mint an API token scoped to R2 + Analytics, and load into Railway env vars. After that, the cron implementation itself is straightforward.

## Parent PRD

`ARCHITECTURE.md` §3 "Retention" + §9 Step 4: "Archive cron at 03:00 UTC (90-day cutoff, R2 upload via S3-compatible client + free-tier guardrail, archive_pointers row, batched deletes)." `deploy.md` §"R2 usage monitoring" for the free-tier guardrail + 80% alerts.

## What to build

### Prep (HITL)

1. Cloudflare R2: create bucket `iter-archive-prod`, enable versioning, lifecycle rule "Infrequent Access at 1 year."
2. Mint an API token scoped to R2 + Analytics (read-only).
3. Load into Railway production env vars: `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_ENDPOINT`, `R2_ARCHIVE_BUCKET=iter-archive-prod`, `R2_REGION=auto`, `CLOUDFLARE_API_TOKEN`. Use `scripts/load-railway-env.sh` (already in repo) — never paste the secret in chat.

### Implementation (AFK after prep is done)

A long-running goroutine in the Go binary (NOT Modal — Railway cron in-process is fine for this) registered on a 03:00 UTC cron:

1. Connect via `DATABASE_URL_BATCH` (iter_batch role — BYPASSRLS so the archiver sees all tenants in one pass).
2. Select all sessions with `started_at < now() - interval '90 days' AND archived_at IS NULL`.
3. For each batch of 100:
   - Bundle session + events + embeddings + scores + outcomes into a single tar.zst object.
   - Upload to R2 at `<tenant_id>/<yyyy-mm>/<session_id>.tar.zst` via the AWS SDK pointed at `$R2_ENDPOINT`.
   - Insert `archive_pointers(session_id, tenant_id, object_uri, archived_at)`.
   - Set `sessions.archived_at = now()`.
   - Delete the source rows (events / embeddings / scores / outcomes will cascade with the session, so only sessions need explicit delete after pointer is recorded — verify cascade in test).
4. **Free-tier guardrail** (per `deploy.md` "R2 usage monitoring"): before upload, check current bucket usage via the Cloudflare Analytics API. If projected storage after this batch would exceed `R2_USAGE_ALERT_THRESHOLD * R2_FREE_STORAGE_GB`, PAUSE the batch and emit `r2_usage_threshold_exceeded` audit log entry + BetterStack alert.
5. Idempotency: rerunning the cron over the same 24h window is a no-op (the `archived_at` filter excludes already-archived sessions).
6. Failure handling: a failed upload retries up to 3 times with backoff; persistent failure leaves the session intact (NOT marked archived) — next-day cron picks it up. Audit-log `archive_failed` with the session_id + error.

## Acceptance criteria

- [ ] R2 bucket provisioned with versioning + lifecycle rule (HITL prep — checkbox once done)
- [ ] R2 env vars loaded into Railway production (HITL prep — checkbox once done; verify via `railway variables --service iter-server`)
- [ ] Cron registered at 03:00 UTC; warm-start verified by running once manually with a 1-day cutoff
- [ ] Uses `DATABASE_URL_BATCH` (iter_batch) — superuser URL never appears in archive code path
- [ ] Free-tier guardrail blocks the batch when projected usage > 80% of any free-tier metric; alert fires
- [ ] Cascade-after-archive verified: deleting the source `sessions` row removes its child rows (existing invariant from issue 004)
- [ ] Idempotency: re-running on the same window inserts zero new `archive_pointers` rows
- [ ] Integration test stubs the R2 client (no real network); verifies the full delete-after-upload flow
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by Step 3 storage-layer baseline — sessions + archive_pointers + audit_log repository functions; R2/S3 client abstraction

## User stories addressed

Retention invariant. 90 days hot → R2 cold; keeps Postgres bounded; required for the cost-math in `ARCHITECTURE.md` §2.
