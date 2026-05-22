"""Modal stub for the Iter nightly scoring batch.

This is the no-op stub for ARCHITECTURE.md §9 Step 3 ("Modal account + stub
function"). The real composite-scoring batch — pulling session signals from
Postgres via the privileged ``iter_batch`` BYPASSRLS role and writing
``session_scores`` rows — lands in Step 4 (issue 046). The v1 composite
formula itself is already recorded in DECISIONS.md and implemented in Go at
``internal/scoring`` (issue 008); the nightly Modal job will compute over
windowed historical data, not re-derive the per-session arithmetic.

This file deliberately does:

* import ``modal`` and declare a single ``modal.App`` named ``iter-scoring``;
* expose one ``@app.function`` (``stub_score``) returning a deterministic dict
  of the documented shape so the Go cloud binary can smoke-test the wiring;
* avoid any database, network, LLM, or secret access — running this stub
  must not require ``DATABASE_URL_BATCH``, ``MODAL_TOKEN_ID``, or any
  Modal Secret.

The module is also importable as plain Python (``python -c 'import
scoring'``) so the local ``make modal-test`` smoke check can assert the
return shape without contacting Modal.
"""

from __future__ import annotations

from datetime import datetime, timezone
from typing import TypedDict

import modal

# Scorer version is part of the wire shape so the Go side can branch on it
# (e.g. ignore stub scores when computing dashboard rollups). Bump on every
# behaviour change; do NOT reuse a version string with a changed formula.
SCORER_VERSION = "v0-stub"

APP_NAME = "iter-scoring"

# Minimal image — pinned Python so deploys are reproducible. No extra deps
# required for the stub; the real scorer (issue 046) will add psycopg /
# pgvector packages here.
image = modal.Image.debian_slim(python_version="3.12")

app = modal.App(name=APP_NAME, image=image)


class StubScore(TypedDict):
    """Return shape contract for ``stub_score``.

    Mirrors what the real ``ScoreResult`` will look like in issue 046 minus
    the ``signals`` dict and ``composite_breakdown``. Kept narrow so the
    smoke test can match exactly.
    """

    session_id: str
    score: float
    scorer_version: str
    ts: str
    status: str


def _build_stub_score(session_id: str) -> StubScore:
    """Pure builder so the test suite can call it without Modal."""
    return {
        "session_id": session_id,
        "score": 0.0,
        "scorer_version": SCORER_VERSION,
        "ts": datetime.now(timezone.utc).isoformat(),
        "status": "stub",
    }


@app.function()
def stub_score(session_id: str) -> StubScore:
    """No-op scorer.

    Returns a deterministic-shape dict so a remote ``modal run`` or
    ``Function.lookup`` smoke test from the Go binary can verify deploy
    succeeded end-to-end. No DB, no LLM, no secrets.
    """
    return _build_stub_score(session_id)


@app.local_entrypoint()
def main(session_id: str = "smoke-test") -> None:
    """Allow ``modal run modal/scoring.py`` for an interactive smoke test."""
    result = stub_score.remote(session_id)
    print(result)
