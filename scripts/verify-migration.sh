#!/usr/bin/env bash
# Smoke-test the initial migration. Confirms tables, extensions, HNSW index
# parameters, and RLS policies match ARCHITECTURE.md §3 and CLAUDE.md invariants.
#
# Run via:  make db-verify
# Or directly: scripts/verify-migration.sh "$DATABASE_URL"
#
# This is NOT the cascade-delete / RLS isolation test — that lives in issue 004.

set -euo pipefail

DATABASE_URL="${1:-${DATABASE_URL:-}}"
if [[ -z "$DATABASE_URL" ]]; then
  echo "usage: verify-migration.sh <database-url>" >&2
  exit 2
fi

psql_q() { psql "$DATABASE_URL" -At -c "$1"; }

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

# 1. Postgres major version >= 16
ver="$(psql_q "SHOW server_version_num;")"
[[ "$ver" -ge 160000 ]] || fail "Postgres < 16 ($ver)"
pass "postgres version $ver"

# 2. Extensions installed
for ext in pgcrypto vector citext; do
  found="$(psql_q "SELECT 1 FROM pg_extension WHERE extname='$ext';")"
  [[ "$found" == "1" ]] || fail "extension '$ext' not installed"
  pass "extension $ext"
done

# 3. All expected tables present
expected_tables=(
  tenants users tenant_users
  sessions session_events session_embeddings session_scores
  outcomes suggestions stacks stack_shares
  archive_pointers audit_log
  scoring_batch_runs
  pending_outcomes
)
for t in "${expected_tables[@]}"; do
  found="$(psql_q "SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='$t' AND c.relkind='r';")"
  [[ "$found" == "1" ]] || fail "missing table: $t"
  pass "table $t"
done

# 4. HNSW indexes exist with m=16, ef_construction=64
hnsw_ok="$(psql_q "
  SELECT count(*) FROM pg_class c
  JOIN pg_index i ON i.indexrelid = c.oid
  JOIN pg_am a ON a.oid = c.relam
  WHERE c.relname IN ('idx_embeddings_hnsw','idx_suggestions_embedding')
    AND a.amname = 'hnsw'
    AND c.reloptions::text LIKE '%m=16%'
    AND c.reloptions::text LIKE '%ef_construction=64%';
")"
[[ "$hnsw_ok" == "2" ]] || fail "HNSW indexes missing or wrong params (found $hnsw_ok of 2)"
pass "HNSW indexes (m=16, ef_construction=64)"

# 5. RLS enabled on every tenant-scoped table
rls_tables=(
  sessions session_events session_embeddings session_scores
  outcomes suggestions stacks stack_shares archive_pointers audit_log
)
for t in "${rls_tables[@]}"; do
  enabled="$(psql_q "SELECT relrowsecurity FROM pg_class WHERE relname='$t';")"
  [[ "$enabled" == "t" ]] || fail "RLS not enabled on $t"
  policy="$(psql_q "SELECT 1 FROM pg_policies WHERE schemaname='public' AND tablename='$t' AND policyname='tenant_isolation';")"
  [[ "$policy" == "1" ]] || fail "tenant_isolation policy missing on $t"
  pass "RLS+policy $t"
done

# 6. iter_batch role exists with BYPASSRLS
batch="$(psql_q "SELECT rolbypassrls FROM pg_roles WHERE rolname='iter_batch';")"
[[ "$batch" == "t" ]] || fail "iter_batch role missing or lacks BYPASSRLS"
pass "iter_batch role has BYPASSRLS"

# 7. Re-running migrations is a no-op
status_before="$(psql_q "SELECT count(*) FROM goose_db_version;")"
goose -dir migrations postgres "$DATABASE_URL" up >/dev/null
status_after="$(psql_q "SELECT count(*) FROM goose_db_version;")"
[[ "$status_before" == "$status_after" ]] || fail "re-run of goose up changed version count ($status_before -> $status_after)"
pass "re-running migrations is idempotent"

echo ""
echo "All schema invariants verified."
