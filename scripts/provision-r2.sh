#!/usr/bin/env bash
#
# provision-r2.sh — one-shot Cloudflare R2 bucket + token provisioning for
# the Iter archive cron (issue 047).
#
# Idempotent: safe to re-run. Creates (or no-ops on existing) the
# `iter-archive-prod` bucket, enables object versioning, installs a
# 1-year-to-Infrequent-Access lifecycle rule, mints an S3-compatible API
# token scoped to the bucket, and pushes every R2_* env var to Railway
# (production environment by default; override RAILWAY_ENV).
#
# PREREQUISITES (operator-side, NOT script-checked at boot but documented
# so a failed run points at the right control panel):
#
#   - wrangler CLI installed + authed (`wrangler whoami` works)
#   - railway CLI installed + authed (`railway whoami` works)
#   - R2 enabled on the Cloudflare account (one-time billing acknowledged)
#   - jq + curl available on PATH
#   - CLOUDFLARE_API_TOKEN exported in the local env. The token must carry
#     **Workers R2 Storage:Edit** + **Account Settings:Read** scopes. Mint
#     it at: https://dash.cloudflare.com/profile/api-tokens
#
# WHY a separate API token (and not wrangler's own auth): the Cloudflare
# `r2/api-tokens` REST endpoint returns AWS-shaped Access Key ID + Secret
# Access Key pairs that the standard AWS SDK can use against
# $R2_ENDPOINT. wrangler does not expose this minting path, so we hit the
# API directly with curl.
#
# Run from the repo root:
#   CLOUDFLARE_API_TOKEN=... ./scripts/provision-r2.sh
#
# Override the bucket name / Railway service / environment via env:
#   R2_ARCHIVE_BUCKET=iter-archive-staging \
#   RAILWAY_ENV=staging \
#     ./scripts/provision-r2.sh

set -euo pipefail

BUCKET="${R2_ARCHIVE_BUCKET:-iter-archive-prod}"
RAILWAY_SERVICE="${RAILWAY_SERVICE:-iter-server}"
RAILWAY_ENV="${RAILWAY_ENV:-production}"

if [[ -z "${CLOUDFLARE_API_TOKEN:-}" ]]; then
  echo "error: CLOUDFLARE_API_TOKEN is required." >&2
  echo "Mint a token at https://dash.cloudflare.com/profile/api-tokens" >&2
  echo "with 'Workers R2 Storage:Edit' + 'Account Settings:Read' scopes," >&2
  echo "then export CLOUDFLARE_API_TOKEN=<token> and re-run." >&2
  exit 2
fi

for bin in wrangler railway curl jq; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "error: required binary not found on PATH: $bin" >&2
    exit 2
  fi
done

# 1. Create the bucket (no-op if it exists). wrangler returns non-zero
#    when the bucket already exists; we treat "already exists" as success
#    so re-runs are idempotent.
echo "==> Creating bucket $BUCKET (if missing)..."
if ! wrangler r2 bucket create "$BUCKET" 2>&1 | tee /dev/stderr | grep -qE "Created|already exists"; then
  echo "error: wrangler r2 bucket create returned unexpected output" >&2
  exit 1
fi

# 2. Enable object versioning. Idempotent — re-enable on an
#    already-versioned bucket is a no-op.
echo "==> Enabling versioning on $BUCKET..."
wrangler r2 bucket versioning enable "$BUCKET"

# 3. Lifecycle rule: transition all objects to Infrequent Access at 365
#    days. 31536000 seconds == 365 days. Costs less for cold reads, no
#    operational complexity for the cron.
echo "==> Installing lifecycle rule (1y -> Infrequent Access) on $BUCKET..."
LIFECYCLE_JSON="$(mktemp -t r2-lifecycle.XXXXXX.json)"
trap 'rm -f "$LIFECYCLE_JSON"' EXIT
cat > "$LIFECYCLE_JSON" <<JSON
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
wrangler r2 bucket lifecycle set "$BUCKET" --file "$LIFECYCLE_JSON"

# 4. Resolve the Cloudflare account ID from `wrangler whoami`. The output
#    is a Unicode box-drawing table; parse the 32-hex-char field.
echo "==> Resolving Cloudflare account ID..."
ACCOUNT_ID="$(wrangler whoami 2>&1 | awk -F'│' '/[a-f0-9]{32}/ {gsub(/ /,"",$3); print $3; exit}')"
if [[ -z "$ACCOUNT_ID" ]]; then
  echo "error: could not extract account ID from 'wrangler whoami'" >&2
  exit 1
fi

# 5. Mint an R2 S3-compatible API token scoped to this bucket. The
#    response carries Access Key ID + Secret Access Key — those are the
#    credentials the AWS SDK will use against $R2_ENDPOINT.
echo "==> Minting R2 S3 token scoped to $BUCKET..."
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

if [[ -z "$ACCESS_KEY_ID" || -z "$SECRET_ACCESS_KEY" || "$ACCESS_KEY_ID" == "null" ]]; then
  echo "error: Cloudflare r2/api-tokens response missing credentials:" >&2
  echo "$TOKEN_JSON" >&2
  exit 1
fi

# 6. Push every R2 env var to Railway. --skip-deploys avoids redeploying
#    on each --set call; the next `railway up` (or webhook-triggered
#    deploy) picks up all of them atomically.
echo "==> Setting Railway env vars (service=$RAILWAY_SERVICE env=$RAILWAY_ENV)..."
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

echo
echo "Done. Bucket $BUCKET ready; Railway $RAILWAY_ENV env vars set."
echo "Next: run scripts/verify-r2.sh to smoke-test the put/get/delete path."
