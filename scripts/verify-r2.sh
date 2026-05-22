#!/usr/bin/env bash
#
# verify-r2.sh — end-to-end smoke test for the R2 bucket provisioned by
# scripts/provision-r2.sh (issue 047).
#
# Puts a 1 KiB dummy object via aws-sdk-go-v2 against `$R2_ENDPOINT`,
# downloads it back, asserts the round-trip is byte-identical, then
# deletes it. The Go program (scripts/verify-r2/main.go) uses the EXACT
# SDK configuration the archive cron uses at runtime, so a green run here
# proves the SDK + endpoint + credentials work as a quartet before the
# cron starts writing.
#
# RECOMMENDED OVER `aws s3api --endpoint-url`: keeping the verify path on
# aws-sdk-go-v2 means there is one source of truth for "how Iter talks to
# R2." A passing `aws s3api` test would not catch e.g. a virtual-host
# vs. path-style addressing mismatch the SDK is configured for.
#
# Reads credentials from env vars (typically populated by
# `railway run --service iter-server -- ./scripts/verify-r2.sh` so the
# Railway-stored vars land in this shell, OR by sourcing a local .env):
#
#   R2_ENDPOINT
#   R2_ACCESS_KEY_ID
#   R2_SECRET_ACCESS_KEY
#   R2_ARCHIVE_BUCKET
#   R2_REGION  (optional; defaults to "auto")

set -euo pipefail

: "${R2_ENDPOINT:?R2_ENDPOINT must be set (run via 'railway run ...' or source a .env)}"
: "${R2_ACCESS_KEY_ID:?R2_ACCESS_KEY_ID must be set}"
: "${R2_SECRET_ACCESS_KEY:?R2_SECRET_ACCESS_KEY must be set}"
: "${R2_ARCHIVE_BUCKET:?R2_ARCHIVE_BUCKET must be set}"
export R2_REGION="${R2_REGION:-auto}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

echo "==> Smoke-testing put/get/delete against bucket $R2_ARCHIVE_BUCKET..."
go run ./scripts/verify-r2
echo "OK: R2 bucket round-trip succeeded."
