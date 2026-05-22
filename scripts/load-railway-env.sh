#!/usr/bin/env bash
# Bulk-load a local .env file into Railway with a single API call.
# Uses --skip-deploys so you can choose when to trigger a redeploy.
#
# Usage:
#   scripts/load-railway-env.sh [env-file] [service] [railway-env]
#
# Defaults: .env.production  iter-server  production
#
# Prereqs (one-time, manual):
#   railway login
#   railway link    # pick the iter project

set -euo pipefail

ENV_FILE="${1:-.env.production}"
SERVICE="${2:-iter-server}"
RAILWAY_ENV="${3:-production}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "error: env file not found: $ENV_FILE" >&2
  exit 1
fi

args=()
while IFS='=' read -r key value; do
  [[ -z "$key" || "$key" == \#* ]] && continue
  # strip surrounding double or single quotes if present
  value="${value%\"}"; value="${value#\"}"
  value="${value%\'}"; value="${value#\'}"
  args+=(--set "$key=$value")
done < "$ENV_FILE"

if [[ ${#args[@]} -eq 0 ]]; then
  echo "error: no variables parsed from $ENV_FILE" >&2
  exit 1
fi

echo "loading $((${#args[@]} / 2)) variables into service=$SERVICE env=$RAILWAY_ENV (no redeploy)"

railway variables \
  --service "$SERVICE" \
  --environment "$RAILWAY_ENV" \
  --skip-deploys \
  "${args[@]}"

echo "done. trigger a deploy with:"
echo "  railway redeploy --service $SERVICE --environment $RAILWAY_ENV"
