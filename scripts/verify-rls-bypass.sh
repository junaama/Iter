#!/usr/bin/env bash
# Demonstrate the tenant-isolation invariant from CLAUDE.md and the
# `iter_batch` BYPASSRLS contract from ARCHITECTURE.md §3:
#
#   * iter_app (NOBYPASSRLS) sees ONLY rows whose tenant_id matches the
#     current `app.current_tenant` GUC.
#   * iter_batch (BYPASSRLS) sees ALL rows regardless of GUC.
#
# This is the canonical sanity check for issue 003.
#
# Usage:
#   scripts/verify-rls-bypass.sh <superuser-url> <iter_app-url> <iter_batch-url>
#
# All three URLs must point at the SAME database (typically Railway prod).
# The superuser URL is required to seed two test tenants and to GRANT
# temporary password-set access; it is otherwise unused at request time.
#
# The script is idempotent and cleans up after itself even on failure.

set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: verify-rls-bypass.sh <superuser-url> <iter_app-url> <iter_batch-url>" >&2
  exit 2
fi

SUPER_URL="$1"
APP_URL="$2"
BATCH_URL="$3"

# Stable, well-known UUIDs so cleanup is unambiguous and the script is rerunnable.
TENANT_A="11111111-1111-1111-1111-111111111111"
TENANT_B="22222222-2222-2222-2222-222222222222"
USER_A="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
USER_B="bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

fail() { echo "FAIL: $*" >&2; cleanup; exit 1; }
pass() { echo "ok:   $*"; }
info() { echo "info: $*"; }

cleanup() {
  info "cleaning up test rows"
  psql "$SUPER_URL" -v ON_ERROR_STOP=0 -q <<SQL >/dev/null 2>&1 || true
    DELETE FROM sessions WHERE tenant_id IN ('$TENANT_A','$TENANT_B');
    DELETE FROM tenant_users WHERE tenant_id IN ('$TENANT_A','$TENANT_B');
    DELETE FROM users WHERE id IN ('$USER_A','$USER_B');
    DELETE FROM tenants WHERE id IN ('$TENANT_A','$TENANT_B');
SQL
}

trap cleanup EXIT

# Sanity-check roles before doing anything destructive.
info "checking role attributes"
batch_bypass=$(psql "$SUPER_URL" -At -c "SELECT rolbypassrls FROM pg_roles WHERE rolname='iter_batch';")
[[ "$batch_bypass" == "t" ]] || fail "iter_batch missing BYPASSRLS (got '$batch_bypass')"
pass "iter_batch has BYPASSRLS"

app_bypass=$(psql "$SUPER_URL" -At -c "SELECT rolbypassrls FROM pg_roles WHERE rolname='iter_app';")
[[ "$app_bypass" == "f" ]] || fail "iter_app must NOT have BYPASSRLS (got '$app_bypass')"
app_super=$(psql "$SUPER_URL" -At -c "SELECT rolsuper FROM pg_roles WHERE rolname='iter_app';")
[[ "$app_super" == "f" ]] || fail "iter_app must NOT be superuser (got '$app_super')"
pass "iter_app is NOSUPERUSER NOBYPASSRLS"

# Seed two tenants + a session in each, as superuser (bypasses RLS for setup).
info "seeding tenants and sessions"
psql "$SUPER_URL" -v ON_ERROR_STOP=1 -q <<SQL >/dev/null
  INSERT INTO tenants (id, name) VALUES
    ('$TENANT_A', 'rls-test-A'),
    ('$TENANT_B', 'rls-test-B')
  ON CONFLICT (id) DO NOTHING;
  INSERT INTO users (id, email, display_name) VALUES
    ('$USER_A', 'rls-a@example.invalid', 'RLS Test A'),
    ('$USER_B', 'rls-b@example.invalid', 'RLS Test B')
  ON CONFLICT (id) DO NOTHING;
  INSERT INTO tenant_users (tenant_id, user_id, role) VALUES
    ('$TENANT_A', '$USER_A', 'owner'),
    ('$TENANT_B', '$USER_B', 'owner')
  ON CONFLICT DO NOTHING;
  INSERT INTO sessions (tenant_id, user_id, harness, model, started_at,
                        redacted_prompt, classification)
  VALUES
    ('$TENANT_A', '$USER_A', 'claude-code', 'sonnet-4.6', now(),
     'rls-test-prompt-A', 'clean'),
    ('$TENANT_B', '$USER_B', 'claude-code', 'sonnet-4.6', now(),
     'rls-test-prompt-B', 'clean');
SQL
pass "seeded 2 tenants, 2 users, 2 sessions"

# --- iter_app sees only its tenant ---
info "iter_app: SET LOCAL app.current_tenant = tenant_A"
count_a=$(psql "$APP_URL" -At <<SQL
  BEGIN;
  SET LOCAL app.current_tenant = '$TENANT_A';
  SELECT count(*) FROM sessions
    WHERE redacted_prompt IN ('rls-test-prompt-A','rls-test-prompt-B');
  COMMIT;
SQL
)
count_a=$(echo "$count_a" | grep -E '^[0-9]+$' | tail -1)
[[ "$count_a" == "1" ]] || fail "iter_app with tenant_A should see exactly 1 test session, saw '$count_a'"
pass "iter_app sees only tenant_A's row (count=1)"

info "iter_app: SET LOCAL app.current_tenant = tenant_B"
count_b=$(psql "$APP_URL" -At <<SQL
  BEGIN;
  SET LOCAL app.current_tenant = '$TENANT_B';
  SELECT count(*) FROM sessions
    WHERE redacted_prompt IN ('rls-test-prompt-A','rls-test-prompt-B');
  COMMIT;
SQL
)
count_b=$(echo "$count_b" | grep -E '^[0-9]+$' | tail -1)
[[ "$count_b" == "1" ]] || fail "iter_app with tenant_B should see exactly 1 test session, saw '$count_b'"
pass "iter_app sees only tenant_B's row (count=1)"

info "iter_app: no current_tenant set → should see 0 rows (or error)"
# Without app.current_tenant set, current_setting()::uuid raises, and the policy
# rejects the row. We tolerate either zero rows OR a clean error.
unset_count=$(psql "$APP_URL" -v ON_ERROR_STOP=0 -At <<SQL 2>/dev/null || echo "error"
  SELECT count(*) FROM sessions
    WHERE redacted_prompt IN ('rls-test-prompt-A','rls-test-prompt-B');
SQL
)
unset_count=$(echo "$unset_count" | { grep -E '^[0-9]+$|error' || true; } | tail -1)
if [[ "$unset_count" != "0" && "$unset_count" != "error" && -n "$unset_count" ]]; then
  fail "iter_app with no current_tenant should see 0 rows or error, got '$unset_count'"
fi
pass "iter_app with no current_tenant returns 0 rows or error"

# --- iter_batch sees both regardless of GUC ---
info "iter_batch: no current_tenant set → should see both rows"
batch_count=$(psql "$BATCH_URL" -At <<SQL
  SELECT count(*) FROM sessions
    WHERE redacted_prompt IN ('rls-test-prompt-A','rls-test-prompt-B');
SQL
)
batch_count=$(echo "$batch_count" | grep -E '^[0-9]+$' | tail -1)
[[ "$batch_count" == "2" ]] || fail "iter_batch should see both test sessions, saw '$batch_count'"
pass "iter_batch sees both rows (count=2)"

info "iter_batch: even with current_tenant = tenant_A, should see both rows"
batch_count_filtered=$(psql "$BATCH_URL" -At <<SQL
  BEGIN;
  SET LOCAL app.current_tenant = '$TENANT_A';
  SELECT count(*) FROM sessions
    WHERE redacted_prompt IN ('rls-test-prompt-A','rls-test-prompt-B');
  COMMIT;
SQL
)
batch_count_filtered=$(echo "$batch_count_filtered" | grep -E '^[0-9]+$' | tail -1)
[[ "$batch_count_filtered" == "2" ]] || fail "iter_batch with current_tenant set should STILL see both rows, saw '$batch_count_filtered'"
pass "iter_batch ignores app.current_tenant (BYPASSRLS works)"

echo ""
echo "All RLS-bypass invariants verified."
