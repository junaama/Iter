# Modal — Iter nightly scoring batch (stub)

This directory ships the **stub** Modal app for Iter's nightly composite-
scoring batch. Real scoring logic (pulling session signals from Postgres
via the `iter_batch` BYPASSRLS role, computing the composite formula
recorded in `DECISIONS.md`, and writing `session_scores` rows) lands in
issue 046. This slice exists so the Go cloud binary can smoke-test the
deploy wiring before there is any scoring code to run.

References:
- `ARCHITECTURE.md` §4 (Workloads) and §9 Step 3 ("Modal account + stub function")
- `DECISIONS.md` — Modal warm pool N=2 (Phase 8); composite scoring formula
- Issue `057-modal-scoring-stub.md`

## Files

| Path | Purpose |
|---|---|
| `scoring.py` | `modal.App("iter-scoring")` with one `@app.function` (`stub_score`). |
| `requirements.txt` | Pinned `modal==1.2.6` + `pytest` for the local smoke test. |
| `scoring_test.py` | Local pytest — no Modal credentials needed. |

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
```

## Local-only smoke test (AFK, no Modal credentials)

`make modal-test` runs `pytest modal/scoring_test.py` from inside `modal/`
so the test file can `import scoring`. It asserts the return shape and
that `scoring.app` is a real `modal.App`. Safe to run in CI once the
Python toolchain is installed there.

## Deploy (HITL — counts against billable Modal usage)

```bash
# From the repo root, against the workspace that owns the iter-scoring app.
modal deploy modal/scoring.py

# Expected output (abridged):
#   ✓ Created objects.
#   ✓ Created function stub_score.
#   ✓ App deployed in <region>! 🎉
#   View Deployment: https://modal.com/apps/<workspace>/main/deployed/iter-scoring
```

`make modal-deploy` is a thin wrapper that runs the same command and
assumes the token is already in place (either `~/.modal.toml` from
`modal token new` or `MODAL_TOKEN_ID` / `MODAL_TOKEN_SECRET` in the
environment).

### Warm pool

ARCHITECTURE.md §8 and DECISIONS.md Phase 8 lock the production warm-pool
size at **N=2**. This stub deploys at the default (on-demand, N=0) because
keeping containers warm for a no-op function would burn budget. When the
real scorer ships in issue 046, redeploy with:

```bash
# Decorator change in modal/scoring.py:
#   @app.function(min_containers=2)
# then:
modal deploy modal/scoring.py
```

Do **not** add `min_containers=2` to the stub.

## Invoking from the Go binary

Once deployed, the Go cloud binary can look up and invoke the function:

```go
// pseudo — real wiring lands in issue 046
fn := modal.LookupFunction("iter-scoring", "stub_score")
res, err := fn.Call(ctx, "smoke-test-session")
```

The Go SDK (`github.com/modal-labs/libmodal/modal-go`) reads the same
`MODAL_TOKEN_ID` / `MODAL_TOKEN_SECRET` env vars as the Python CLI.
