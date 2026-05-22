---
type: AFK
depends-on:
  - 057-modal-scoring-stub
---

## Parent PRD

`ARCHITECTURE.md` §4 + §9 Step 4: "Modal scoring batch at 02:00 UTC (idempotent, version-tagged)." Uses the pure scoring function from issue 008 + the signal aggregator from issue 011.

## What to build

Nightly Modal function that re-scores every session that arrived in the last 24h. Lives outside the Go binary — Python on Modal, importing the score computation as JSON-defined math (the formula is locked per `DECISIONS.md` issue 008 "Composite scoring formula"). The Python side talks to Postgres via the `DATABASE_URL_BATCH` env var (the `iter_batch` BYPASSRLS role from issue 003).

Concretely:

1. `modal/scoring.py` — a Modal `@app.function` decorated with `@modal.Cron("0 2 * * *")` (02:00 UTC daily). Warm pool N=2 per `ARCHITECTURE.md`.
2. The function:
   - Connects via `DATABASE_URL_BATCH` (psycopg or asyncpg). No `SET LOCAL app.current_tenant` — BYPASSRLS sees all tenants in one pass.
   - Pulls every session that has new `session_events` since the last successful run timestamp (`scoring_batch_runs` table — add migration `0003_scoring_batch_runs.sql`).
   - For each session: aggregate signals (port `internal/signals.Aggregate` logic to Python; OR call into a side service — pick one and document). Score via the locked formula. Insert a new `session_scores` row tagged with `scorer_version` = `v0.${MODAL_DEPLOY_SHA}`.
   - Idempotent: same session re-scored under the same scorer_version is a no-op (`INSERT … ON CONFLICT (session_id, scorer_version) DO NOTHING`).
   - On exception: emit a `scoring_batch_failed` audit-log entry; the next-day run picks up everything since the last SUCCESS timestamp.
3. `modal/scoring_test.py` — local-runnable tests (mock the DB) that validate the score-aggregation port matches the Go reference (golden-file comparison: run identical fixtures through both, assert identical outputs).

## Acceptance criteria

- [ ] Modal function registered on a 02:00 UTC cron with warm pool N=2
- [ ] Uses `DATABASE_URL_BATCH` (iter_batch role) — verify by running locally with the iter_app URL and confirming it fails with "permission denied" on cross-tenant rows
- [ ] `scoring_batch_runs` table added in a new migration (`0003_*.sql`); applied to Railway
- [ ] Scorer version tagged on every row; rollback path via `scorer_version` column documented
- [ ] Idempotency verified: re-running the cron for the same window inserts zero new rows
- [ ] Golden-file test: identical fixtures produce identical scores in Go (internal/scoring) and Python (modal/scoring.py)
- [ ] Audit-log entry on failure; success run updates `scoring_batch_runs` only AFTER all sessions in the window are scored
- [ ] `modal deploy modal/scoring.py` succeeds against the Iter Modal workspace
- [ ] `make test` + `make test-rls` + `make lint` pass (Python tests run via `uv` or `pytest` — pick and add to Makefile)

## Blocked by

- Blocked by Step 3 storage-layer baseline — needs `DATABASE_URL_BATCH` wired + provider abstraction for Modal
- Soft-depends on `008-pure-scoring-function-tests` + `011-signal-aggregation-tests` (already done — port their logic)

## User stories addressed

Every dashboard score, every `iter suggest` ranking — all derive from this nightly batch.

## Done note

Landed in this slice:

- `modal/scoring.py` rewritten: `@app.function(secrets=[Secret.from_name("iter-postgres")], schedule=modal.Cron("0 2 * * *"), timeout=3600, min_containers=2)` `nightly_score` cron. psycopg3-sync DB driver.
- `modal/scoring_test.py` extended (40 tests passing): pure-helper unit tests, Go-canonical golden parity for `aggregate_signals` + `composite_score`, mocked-DB tests for idempotent ON CONFLICT, per-session exception isolation, run-row success/failure recording, and `scorer_version` env precedence.
- `modal/testdata/golden_signals.json` generated from `scripts/gen-golden-signals.go` (Go is the canonical reference for both `internal/signals.Aggregate` and `internal/scoring.Composite`).
- `migrations/0005_scoring_batch_runs.sql` adds the run-ledger table. No tenant_id / no RLS; iter_batch-only grants.
- `modal/requirements.txt` adds `psycopg[binary]==3.2.3`. Decision recorded in DECISIONS.md alongside scorer_version naming + windowed-signals ownership.
- `Makefile`: `make modal-test` still runs the same `pytest` (now 40 tests, all green). New `make gen-golden-signals` re-emits the JSON golden.
- `scripts/verify-migration.sh` learns the new table.

HITL items remaining:

1. Apply migration to Railway:
   ```bash
   make migrate-up DATABASE_URL="$DATABASE_PUBLIC_URL_SUPERUSER"
   ```
   (Superuser URL because creating tables exceeds iter_app's grants. Worktree sandbox cannot read Railway env vars; orchestrator runs this.)
2. Create the Modal Secret `iter-postgres` with `DATABASE_URL_BATCH=...` (pointing at iter_batch role).
3. First production deploy switches the `iter-scoring` app from `stub_score` to `nightly_score`:
   ```bash
   modal deploy modal/scoring.py
   ```
   This is HITL — consumes billable Modal compute. Subsequent deploys are automated.
4. Verify the first scheduled run lands a row in `scoring_batch_runs` with `status='succeeded'`.

Punted (not in this slice):

- Real durability-window definition tuning. The placeholder definition in `_select_durability` uses `pr_merged AND NOT pr_reverted/code_reverted_within_7d`; the v1 formula constants are locked but the durability *definition* is owned by the Modal batch and can iterate without re-deploying Go.
- Replay protection for half-finished runs (a 'running' row stuck after a crash). The next-run watermark logic reads only succeeded rows, so a stuck 'running' row is functionally identical to no run — a cleanup query is a future hardening item.
