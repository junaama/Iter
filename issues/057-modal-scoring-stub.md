---
type: HITL+AFK
depends-on: []
---

# Mixed HITL + AFK

Modal account / token creation is HITL. The stub `modal/scoring.py` file + the `modal deploy` smoke test is AFK once the token is in place.

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Modal account + stub function"); §4 "Workloads" — Modal is the nightly scoring batch with a warm pool of N=2 (per `DECISIONS.md` Phase 8). §9 Step 4 expands this to "Modal scoring batch at 02:00 UTC (idempotent, version-tagged)"; this slice ships the no-op stub, not the real batch.

## What to build

A bootable Modal function that the cloud binary can invoke as a smoke test. Real scoring code lands in Step 4.

### HITL — Modal account

1. Create a Modal account; provision a token. Record `MODAL_TOKEN_ID` and `MODAL_TOKEN_SECRET` in Railway env vars per environment (already enumerated in `deploy.md`).
2. Create a Modal app named `iter-scoring`. Warm-pool configuration deferred — this stub does not need N=2 yet.

### AFK — Stub function

1. `modal/scoring.py` exporting a function `stub_score(session_id: str) -> dict` that returns `{"session_id": session_id, "score": 0.0, "scorer_version": "v0-stub", "ts": <utcnow>}`. No DB writes. No LLM calls. Deterministic.
2. `modal/scoring.py` declares its Modal stub with a minimal image (`python:3.12-slim` + `modal`).
3. `modal/README.md` documents how to deploy (`modal deploy modal/scoring.py`) and how to invoke from the Go binary (HTTP endpoint via `@app.function(secret=Secret.from_name("iter-prod"))`).
4. CI/Railway integration: add `modal/` to `.gitignore` exclusions if not already; the file itself is committed. No Modal calls from CI in this slice.
5. **Smoke test** (manual, scripted): `make modal-smoke` runs `python -c "import modal; ..."` to invoke the stub once and asserts the response shape matches. Skipped if `MODAL_TOKEN_ID` is empty.

## Acceptance criteria

### HITL

- [ ] Modal account created; tokens in Railway env vars (dev/staging/prod)
- [ ] `iter-scoring` Modal app exists
- [ ] Warm-pool decision (N=2 vs. on-demand) recorded — N=2 is the Phase 8 decision; if changed, record in `DECISIONS.md`

### AFK

- [ ] `modal/scoring.py` exports `stub_score(session_id)` with the documented return shape
- [ ] `modal deploy modal/scoring.py` succeeds (manual run, captured in PR description)
- [ ] `make modal-smoke` target invokes the stub and asserts shape; skipped without token
- [ ] `modal/README.md` documents deploy + invoke patterns
- [ ] No DB or LLM dependencies in the stub

## Blocked by

None — can start immediately (the Go binary doesn't need to call Modal yet).

## User stories addressed

Foundation for the nightly scoring batch. Until the real scorer lands in Step 4, the dashboard "first scored session estimated in <X> hours" empty state is the only user-visible touchpoint.
