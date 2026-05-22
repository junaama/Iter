# Modal — Iter nightly scoring batch

This directory ships the production Modal app for Iter's nightly
composite-scoring batch. The first deploy after issue 046 lands
switches the `iter-scoring` app from the no-op stub (issue 057) to the
real batch: pulls every session with new `session_events` since the
last successful run, aggregates per-session signals, computes the
locked v1 composite (`DECISIONS.md` "Composite scoring formula (v1)"),
augments with the windowed signals (`durability_7d`, `durability_30d`),
and writes an idempotent `session_scores` row per session.

References:
- `ARCHITECTURE.md` §4 (Workloads) and §9 Step 4 ("Modal scoring batch at 02:00 UTC")
- `DECISIONS.md`:
  - "Composite scoring formula (v1)" — locked weights + transform
  - "Modal SDK pin + Python version (issue 057)" — `modal==1.2.6`, Python 3.12
  - "DB driver for Modal batch (issue 046)" — `psycopg[binary]` 3.x
  - "Scoring batch runs table (issue 046)" — `scoring_batch_runs` purpose + ownership
  - "Windowed signals owner (issue 046)" — `durability_*` is Modal-only
- Issues `046-modal-scoring-batch.md`, `057-modal-scoring-stub.md`

## Files

| Path | Purpose |
|---|---|
| `scoring.py` | `modal.App("iter-scoring")` — `nightly_score` scheduled function + pure helpers. |
| `requirements.txt` | Pinned `modal`, `psycopg[binary]`, `pytest`. |
| `scoring_test.py` | Local pytest: pure helpers + Go-golden parity + mocked DB layer. |
| `testdata/golden_signals.json` | Generated from `scripts/gen-golden-signals.go`. Do not hand-edit. |

## One-time setup (HITL)

```bash
# 1. Install uv (https://docs.astral.sh/uv/) if you don't have it.
brew install uv

# 2. Create a venv and install pinned deps.
uv venv .venv
source .venv/bin/activate
uv pip install -r modal/requirements.txt

# 3. Authenticate with Modal. Opens a browser; pastes a token into ~/.modal.toml.
#    Run this once per laptop. CI uses MODAL_TOKEN_ID / MODAL_TOKEN_SECRET env vars
#    instead (set in Railway per environment — see deploy.md).
modal token new

# 4. Create the Modal Secret named `iter-postgres` (HITL — one-time per
#    Modal workspace; see deploy.md for the value). The Secret MUST
#    contain DATABASE_URL_BATCH pointing at the iter_batch BYPASSRLS
#    Railway role.
modal secret create iter-postgres DATABASE_URL_BATCH=<paste>
```

## Local-only tests (AFK, no Modal credentials)

`make modal-test` runs `pytest modal/scoring_test.py` from inside
`modal/` so the test file can `import scoring`. It exercises:

- The pure aggregator + composite via a Go-canonical golden fixture
  (regenerable via `make gen-golden-signals`).
- The DB layer via mocked psycopg connections — verifies idempotent
  ON CONFLICT semantics, per-session exception isolation, and the
  `scoring_batch_runs` success / failure rows.

Safe to wire into CI once the Python toolchain is installed there. Does
not contact Modal, Postgres, or any network endpoint.

## Deploy (HITL — counts against billable Modal usage; first deploy switches stub → real)

```bash
# From the repo root, against the workspace that owns the iter-scoring app.
modal deploy modal/scoring.py

# Expected output (abridged):
#   ✓ Created objects.
#   ✓ Created function nightly_score.
#   ✓ App deployed in <region>! 🎉
#   View Deployment: https://modal.com/apps/<workspace>/main/deployed/iter-scoring
```

`make modal-deploy` is a thin wrapper that runs the same command and
assumes the token is already in place (either `~/.modal.toml` from
`modal token new` or `MODAL_TOKEN_ID` / `MODAL_TOKEN_SECRET` in the
environment).

> **Heads-up:** the first `modal deploy` after issue 046 lands replaces
> the no-op `stub_score` function with the real `nightly_score`. The
> function name changes, so any Go-side code looking up
> `iter-scoring.stub_score` must be updated in the same release.

### Warm pool

`ARCHITECTURE.md` §8 and `DECISIONS.md` Phase 8 lock the production
warm-pool size at **N=2**. The scheduled function declares
`min_containers=2` directly in `scoring.py`, so the warm pool comes up
automatically on first deploy. No manual pool configuration needed.

### Cron

The cron is `0 2 * * *` (02:00 UTC). Modal's scheduler reads the
decorator on each deploy; rolling back a deploy reverts the schedule.

### Scorer version + rollback

Every row written to `session_scores` is tagged with
`scorer_version = v1-modal-<sha>` where `<sha>` is the first 12 chars
of `MODAL_DEPLOY_SHA` (or `GIT_SHA` / `RAILWAY_GIT_COMMIT_SHA` as
fallbacks) — see DECISIONS.md "Scorer version naming convention".
Rolling back is "deploy the prior commit"; the older `scorer_version`
re-runs read from `scoring_batch_runs` and pick up everything since the
most-recent succeeded row. No data is overwritten.

## Migration prerequisites

The batch reads from `scoring_batch_runs`, which lives in migration
`0005_scoring_batch_runs.sql`. Apply against Railway before the first
deploy:

```bash
# Local-via-Railway: use the superuser DATABASE_URL (table creation
# requires more than the iter_app grants).
make migrate-up DATABASE_URL="$DATABASE_PUBLIC_URL_SUPERUSER"
```

The unique constraint `session_scores(session_id, scorer_version)`
that backs `ON CONFLICT DO NOTHING` ships in migration
`0004_score_outcome_dedup.sql` (issue 052) — verify with
`make migrate-status` before deploying the scorer.

## Invoking from the Go binary

The Go cloud binary does not invoke this function on the request path
— it is scheduled by Modal's cron. The dashboard backend reads
`session_scores` directly via the iter_app role (RLS-scoped).
