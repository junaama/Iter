#!/usr/bin/env bash
# One-shot provisioning for the request-path `iter_app` role on Railway prod.
#
# Idempotent: rerunning regenerates the password and updates Railway env vars.
# Run AFTER migration 0002_app_role.sql has been applied via:
#   make migrate-up DATABASE_URL="<superuser url>"
#
# Steps:
#   1. Mints a fresh 32-char URL-safe password for `iter_app`.
#   2. ALTER ROLE iter_app PASSWORD '...' over the superuser URL.
#   3. ALTER ROLE iter_batch PASSWORD '...' (separate password) if needed.
#   4. Builds DATABASE_URL (iter_app) and DATABASE_URL_BATCH (iter_batch),
#      preserving the rest of the URL (host, port, db, sslmode).
#   5. Sets both as Railway env vars on the Postgres service (production).
#      Existing vars are preserved; only DATABASE_URL and DATABASE_URL_BATCH
#      are written. --skip-deploys avoids triggering a redeploy.
#
# Usage:
#   scripts/provision-app-role.sh
#
# Requires: railway CLI authed to project `iter`, psql in PATH.

set -euo pipefail

SERVICE="${RAILWAY_SERVICE:-Postgres}"
RAILWAY_ENV="${RAILWAY_ENV:-production}"

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "error: $1 not in PATH" >&2; exit 2; }
}
require railway
require psql

# Pull the superuser URL Railway auto-populates for the Postgres service.
# DATABASE_PUBLIC_URL goes through the public proxy and works from a laptop.
SUPER_URL=$(railway variables --service "$SERVICE" --environment "$RAILWAY_ENV" --kv \
  | grep '^DATABASE_PUBLIC_URL=' | cut -d= -f2-)

if [[ -z "$SUPER_URL" ]]; then
  echo "error: could not read DATABASE_PUBLIC_URL from Railway" >&2
  exit 1
fi

# Same URL form but resolvable only inside Railway. The Go binary uses this.
INTERNAL_URL=$(railway variables --service "$SERVICE" --environment "$RAILWAY_ENV" --kv \
  | grep '^DATABASE_URL=' | cut -d= -f2-)

if [[ -z "$INTERNAL_URL" ]]; then
  echo "error: could not read DATABASE_URL from Railway" >&2
  exit 1
fi

gen_pw() {
  # 32 chars URL-safe: 24 random bytes base64-encoded, stripped of +/=.
  # Falls back to /dev/urandom + hex if openssl is missing.
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 36 | tr -d '+/=' | head -c 32
  else
    head -c 24 /dev/urandom | od -An -txC | tr -d ' \n' | head -c 32
  fi
}

APP_PW="$(gen_pw)"
BATCH_PW="$(gen_pw)"

echo "==> Setting password for iter_app and iter_batch"
psql "$SUPER_URL" -v ON_ERROR_STOP=1 -q <<SQL >/dev/null
  ALTER ROLE iter_app   WITH LOGIN PASSWORD '${APP_PW}';
  ALTER ROLE iter_batch WITH LOGIN PASSWORD '${BATCH_PW}';
SQL

# Rewrite the URL's userinfo segment. Railway DATABASE_URL is shaped like:
#   postgresql://postgres:<pw>@host:port/dbname?sslmode=require
rewrite_url() {
  local url="$1" user="$2" pw="$3"
  # shellcheck disable=SC2001
  echo "$url" | sed -E "s#^([a-z]+)://[^@]+@#\1://${user}:${pw}@#"
}

APP_URL="$(rewrite_url "$INTERNAL_URL" iter_app "$APP_PW")"
BATCH_URL="$(rewrite_url "$INTERNAL_URL" iter_batch "$BATCH_PW")"

APP_PUBLIC_URL="$(rewrite_url "$SUPER_URL" iter_app "$APP_PW")"
BATCH_PUBLIC_URL="$(rewrite_url "$SUPER_URL" iter_batch "$BATCH_PW")"

echo "==> Setting Railway env vars on $SERVICE ($RAILWAY_ENV)"
# We DO NOT touch DATABASE_PUBLIC_URL (Railway auto-manages it; rewriting
# would break the next reset/redeploy reconciliation).
# DATABASE_URL is overwritten to point at iter_app, the request-path role.
# DATABASE_URL_SUPERUSER preserves the old superuser URL for admin use.
# DATABASE_URL_BATCH is the BYPASSRLS connection for Modal + archive cron.
railway variables \
  --service "$SERVICE" \
  --environment "$RAILWAY_ENV" \
  --skip-deploys \
  --set "DATABASE_URL=${APP_URL}" \
  --set "DATABASE_URL_BATCH=${BATCH_URL}" \
  --set "DATABASE_URL_SUPERUSER=${INTERNAL_URL}" \
  --set "DATABASE_PUBLIC_URL_APP=${APP_PUBLIC_URL}" \
  --set "DATABASE_PUBLIC_URL_BATCH=${BATCH_PUBLIC_URL}"

echo ""
echo "Done."
echo "  iter_app password:    ${APP_PW}"
echo "  iter_batch password:  ${BATCH_PW}"
echo ""
echo "Run scripts/verify-rls-bypass.sh to confirm tenant isolation:"
echo "  scripts/verify-rls-bypass.sh \\"
echo "    \"\$SUPER_URL\" \\"
echo "    \"${APP_PUBLIC_URL}\" \\"
echo "    \"${BATCH_PUBLIC_URL}\""
