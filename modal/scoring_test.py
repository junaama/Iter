"""Unit tests for the Modal scoring stub.

These tests run locally without Modal credentials. They exercise the pure
builder (``_build_stub_score``) and verify the module imports cleanly so
``modal deploy`` won't fail on a syntax error or a missing symbol.

The actual ``@app.function`` round-trip against Modal is a manual smoke
test documented in modal/README.md and is **not** wired into CI — it
requires ``MODAL_TOKEN_ID`` / ``MODAL_TOKEN_SECRET`` and counts against
billable Modal usage.
"""

from __future__ import annotations

import datetime as _dt

import scoring


def test_module_exposes_app_and_function() -> None:
    assert scoring.APP_NAME == "iter-scoring"
    assert scoring.SCORER_VERSION == "v0-stub"
    # @app.function-decorated callable must exist as an attribute.
    assert hasattr(scoring, "stub_score")
    # The app object must be a modal.App, not a stub.
    import modal

    assert isinstance(scoring.app, modal.App)


def test_build_stub_score_shape() -> None:
    result = scoring._build_stub_score("session-abc")

    assert set(result.keys()) == {
        "session_id",
        "score",
        "scorer_version",
        "ts",
        "status",
    }
    assert result["session_id"] == "session-abc"
    assert result["score"] == 0.0
    assert result["scorer_version"] == "v0-stub"
    assert result["status"] == "stub"

    # ts must be a parseable ISO-8601 timestamp in UTC.
    parsed = _dt.datetime.fromisoformat(result["ts"])
    assert parsed.tzinfo is not None
    assert parsed.utcoffset() == _dt.timedelta(0)


def test_build_stub_score_is_deterministic_shape() -> None:
    # Two calls with the same id differ only in ts; everything else stable.
    a = scoring._build_stub_score("s1")
    b = scoring._build_stub_score("s1")
    for key in ("session_id", "score", "scorer_version", "status"):
        assert a[key] == b[key]
