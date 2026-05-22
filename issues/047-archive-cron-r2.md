---
type: AFK
depends-on:
  - 018-repositories-suggestions-stacks-archive
---

## Parent PRD

`ARCHITECTURE.md` §3 "Retention" + §9 Step 4: "Archive cron at 03:00 UTC (90-day cutoff, R2 upload via S3-compatible client + free-tier guardrail, archive_pointers row, batched deletes)." `deploy.md` §"R2 usage monitoring" for the free-tier guardrail + 80% alerts.

## Prerequisites (already satisfied — do NOT block on these)

- `wrangler` CLI installed and authed as `0naama0@gmail.com` (Account ID `4ace0ec5e12a94b0631e30a865ec75cc`)
- `railway` CLI authed, linked to project `iter`
- R2 already enabled on the Cloudflare account (one-time billing acknowledgment done)

If `wrangler whoami` ever errors in your environment, run `wrangler login` and `railway login` first.

## What to build

Two scripts and one cron worker, all AFK.

### A. `scripts/provision-r2.sh` — one-shot bucket + token provisioning

Run once per environment. Idempotent (safe to re-run).

```bash
#!/usr/bin/env bash
set -euo pipefail

BUCKET="${R2_ARCHIVE_BUCKET:-iter-archive-prod}"
RAILWAY_SERVICE="${RAILWAY_SERVICE:-iter-server}"
RAILWAY_ENV="${RAILWAY_ENV:-production}"

# 1. Create the bucket (no-op if it exists).
wrangler r2 bucket create "$BUCKET" 2>&1 | tee /dev/stderr | grep -qE "Created|already exists"

# 2. Enable object versioning.
wrangler r2 bucket versioning enable "$BUCKET"

# 3. Lifecycle rule: transition to Infrequent Access at 365 days.
#    See `wrangler r2 bucket lifecycle --help` for the exact JSON shape.
cat > /tmp/r2-lifecycle.json <<JSON
{
  "rules": [
    {
      "id": "ia-at-1y",
      "enabled": true,
      "conditions": { "prefix": "" },
      "transitions": [
        { "condition": { "type": "Age", "maxAge": 31536000 }, "storageClass": "InfrequentAccess" }
      ]
    }
  ]
}
JSON
wrangler r2 bucket lifecycle set "$BUCKET" --file /tmp/r2-lifecycle.json
rm /tmp/r2-lifecycle.json

# 4. Mint an S3-compatible API token scoped to this bucket. Cloudflare's
#    `r2/api-tokens` endpoint returns Access Key ID + Secret Access Key
#    that the AWS SDK can use against `$R2_ENDPOINT`.
ACCOUNT_ID="$(wrangler whoami 2>&1 | awk -F'│' '/[a-f0-9]{32}/ {gsub(/ /,"",$3); print $3; exit}')"
TOKEN_JSON="$(curl -fsS -X POST \
  "https://api.cloudflare.com/client/v4/accounts/${ACCOUNT_ID}/r2/api-tokens" \
  -H "Authorization: Bearer ${CLOUDFLARE_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "$(jq -nc --arg b "$BUCKET" '{
        name: "iter-archive-prod-rw",
        policies: [{permission:"object-read-and-write", buckets:[$b]}]
      }')")"
ACCESS_KEY_ID="$(jq -r '.result.accessKeyId' <<<"$TOKEN_JSON")"
SECRET_ACCESS_KEY="$(jq -r '.result.secretAccessKey' <<<"$TOKEN_JSON")"

# 5. Push every R2 env var to Railway. --skip-deploys avoids a redeploy
#    until the binary is ready to consume them.
railway variables \
  --service "$RAILWAY_SERVICE" \
  --environment "$RAILWAY_ENV" \
  --skip-deploys \
  --set "R2_ACCOUNT_ID=${ACCOUNT_ID}" \
  --set "R2_ACCESS_KEY_ID=${ACCESS_KEY_ID}" \
  --set "R2_SECRET_ACCESS_KEY=${SECRET_ACCESS_KEY}" \
  --set "R2_ENDPOINT=https://${ACCOUNT_ID}.r2.cloudflarestorage.com" \
  --set "R2_ARCHIVE_BUCKET=${BUCKET}" \
  --set "R2_REGION=auto" \
  --set "R2_FREE_STORAGE_GB=10" \
  --set "R2_FREE_CLASS_A_OPS=1000000" \
  --set "R2_FREE_CLASS_B_OPS=10000000" \
  --set "R2_USAGE_ALERT_THRESHOLD=0.80"

echo "Done. Bucket $BUCKET ready; Railway $RAILWAY_ENV env vars set."
```

The script needs `CLOUDFLARE_API_TOKEN` in the local environment (a parent user token with `Workers R2 Storage:Edit` + `Account Settings:Read` scope). If it isn't set, the script should fail with a clear message pointing the operator at <https://dash.cloudflare.com/profile/api-tokens>. Document this prerequisite at the top of the script.

### B. `scripts/verify-r2.sh` — smoke-test the bucket end-to-end

Reads the Railway-stored credentials, uploads a 1 KiB dummy object, GETs it back, asserts equality, deletes it. Used after `provision-r2.sh` to confirm the AWS SDK + endpoint + credentials work as a quartet before the cron starts running.

### C. The archive cron itself (the Go code)

Long-running goroutine in the Go binary (NOT Modal — Railway cron in-process is fine), registered on a 03:00 UTC cron:

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

- [ ] `scripts/provision-r2.sh` runs end-to-end; `wrangler r2 bucket list` shows `iter-archive-prod` with versioning + lifecycle
- [ ] `scripts/verify-r2.sh` passes (put / get / delete a 1 KiB object via the AWS SDK against the live bucket)
- [ ] Railway production env vars set: `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_ENDPOINT`, `R2_ARCHIVE_BUCKET`, `R2_REGION`, plus the four free-tier guardrail vars
- [ ] Cron registered at 03:00 UTC; warm-start verified by running once manually with a 1-day cutoff
- [ ] Uses `DATABASE_URL_BATCH` (iter_batch) — superuser URL never appears in archive code path
- [ ] Free-tier guardrail blocks the batch when projected usage > 80% of any free-tier metric; alert fires
- [ ] Cascade-after-archive verified: deleting the source `sessions` row removes its child rows (existing invariant from issue 004)
- [ ] Idempotency: re-running on the same window inserts zero new `archive_pointers` rows
- [ ] Integration test stubs the R2 client (no real network); verifies the full delete-after-upload flow
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/018-repositories-suggestions-stacks-archive.md` — sessions + archive_pointers + audit_log repository functions; R2/S3 client abstraction

## User stories addressed

Retention invariant. 90 days hot → R2 cold; keeps Postgres bounded; required for the cost-math in `ARCHITECTURE.md` §2.
