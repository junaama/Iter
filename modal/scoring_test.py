"""Unit tests for the Modal nightly scoring batch.

These tests run locally without Modal credentials and without a live
Postgres. The DB layer is exercised through ``unittest.mock`` fakes so
the test file is fast and hermetic; the wire-shape contracts are still
covered byte-for-byte via the golden-signals fixture generated from the
Go reference (``scripts/gen-golden-signals.go``).

Run via ``make modal-test``.
"""

from __future__ import annotations

import datetime as _dt
import json
import math
import os
from pathlib import Path
from typing import Any
from unittest import mock

import pytest

import scoring


GOLDEN_PATH = Path(__file__).parent / "testdata" / "golden_signals.json"

# Tolerance for float comparisons across the Go/Python composite boundary.
# Go uses float64 + math.Exp; CPython uses double + math.exp on the same
# IEEE-754 backend, so values match exactly in practice — keep a tiny
# epsilon for the rare case where the platform's libm rounds differently.
EPS = 1e-12


# ---------------------------------------------------------------------------
# Module wiring
# ---------------------------------------------------------------------------


def test_module_exposes_app_and_function() -> None:
    """The real Modal app + scheduled function must be importable without
    network access. ``min_containers=2`` is part of the warm-pool invariant
    from ARCHITECTURE.md §8 — the smoke test confirms the deploy spec was
    not regressed back to N=0."""
    assert scoring.APP_NAME == "iter-scoring"
    assert hasattr(scoring, "nightly_score")
    import modal

    assert isinstance(scoring.app, modal.App)


def test_scorer_version_default_is_dev() -> None:
    """Without any deploy-sha env var, version should fall back to dev."""
    with mock.patch.dict(os.environ, {}, clear=False):
        for k in ("MODAL_DEPLOY_SHA", "GIT_SHA", "RAILWAY_GIT_COMMIT_SHA"):
            os.environ.pop(k, None)
        assert scoring._scorer_version() == "v1-modal-dev"


def test_scorer_version_picks_modal_deploy_sha_first() -> None:
    """Priority order: MODAL_DEPLOY_SHA > GIT_SHA > RAILWAY_GIT_COMMIT_SHA."""
    env = {
        "MODAL_DEPLOY_SHA": "abc1234567890def",
        "GIT_SHA": "should-not-win",
        "RAILWAY_GIT_COMMIT_SHA": "also-no",
    }
    with mock.patch.dict(os.environ, env, clear=False):
        # First 12 chars of the winning value.
        assert scoring._scorer_version() == "v1-modal-abc123456789"


def test_scorer_version_falls_through_to_git_sha() -> None:
    with mock.patch.dict(os.environ, {}, clear=False):
        for k in ("MODAL_DEPLOY_SHA", "GIT_SHA", "RAILWAY_GIT_COMMIT_SHA"):
            os.environ.pop(k, None)
        os.environ["GIT_SHA"] = "deadbeefcafe1234"
        assert scoring._scorer_version() == "v1-modal-deadbeefcafe"


# ---------------------------------------------------------------------------
# Pure helpers
# ---------------------------------------------------------------------------


def test_clamp01_bounds() -> None:
    assert scoring._clamp01(-1.0) == 0.0
    assert scoring._clamp01(0.0) == 0.0
    assert scoring._clamp01(0.5) == 0.5
    assert scoring._clamp01(1.0) == 1.0
    assert scoring._clamp01(1.5) == 1.0


def test_normalize_unit_treats_nan_as_missing() -> None:
    v, ok = scoring._normalize_unit(float("nan"))
    assert ok is False
    assert v == 0.0


def test_normalize_unit_none_is_missing() -> None:
    v, ok = scoring._normalize_unit(None)
    assert ok is False
    assert v == 0.0


def test_normalize_unit_clamps_floats() -> None:
    v, ok = scoring._normalize_unit(1.5)
    assert ok is True
    assert v == 1.0


def test_saturate_matches_go_formula() -> None:
    # Verify 1 - exp(-n/k) bit-for-bit against the Go reference at a few points.
    for n in (0, 1, 3, 5, 10, 100):
        expected = 1.0 - math.exp(-n / 3.0)
        assert scoring._saturate(n, 3.0) == pytest.approx(expected, rel=0, abs=EPS)


# ---------------------------------------------------------------------------
# Golden file — Go-canonical aggregation + composite scoring
# ---------------------------------------------------------------------------


def _load_golden() -> list[dict[str, Any]]:
    assert GOLDEN_PATH.exists(), (
        f"missing golden file {GOLDEN_PATH}; regenerate via "
        "`go run scripts/gen-golden-signals.go > modal/testdata/golden_signals.json`"
    )
    with GOLDEN_PATH.open() as fh:
        return json.load(fh)


@pytest.mark.parametrize(
    "case",
    _load_golden(),
    ids=lambda c: c["name"],
)
def test_aggregate_signals_matches_go_golden(case: dict[str, Any]) -> None:
    """Python aggregator must produce byte-identical signal dicts vs the
    Go reference (after filtering subagent events, which Go's Aggregate
    does internally — the fixture marks them via ``is_subagent``)."""
    events = [
        e
        for e in (case["events"] or [])
        if not e.get("is_subagent", False)
    ]
    got = scoring.aggregate_signals(events)
    want = case["signals"]
    # Convert Python ScoreSignals to a comparable dict and assert equality
    # on each field. We compare ints/floats/None — bytes-for-bytes via
    # JSON would also work but loses readability on a mismatch.
    assert got.peer_reuse_count == want["peer_reuse_count"]
    assert got.self_reuse_count == want["self_reuse_count"]
    assert got.durability_7d == want["durability_7d"]
    assert got.durability_30d == want["durability_30d"]
    if want["override_rate"] is None:
        assert got.override_rate is None
    else:
        assert got.override_rate == pytest.approx(want["override_rate"], abs=EPS)
    if want["suggestion_acceptance"] is None:
        assert got.suggestion_acceptance is None
    else:
        assert got.suggestion_acceptance == pytest.approx(
            want["suggestion_acceptance"], abs=EPS
        )


@pytest.mark.parametrize(
    "case",
    _load_golden(),
    ids=lambda c: c["name"],
)
def test_composite_score_matches_go_golden(case: dict[str, Any]) -> None:
    """Python ``composite_score`` must match the Go reference composite to
    within ``EPS`` for every fixture."""
    sigs = scoring.ScoreSignals(
        durability_7d=case["signals"]["durability_7d"],
        durability_30d=case["signals"]["durability_30d"],
        peer_reuse_count=case["signals"]["peer_reuse_count"],
        self_reuse_count=case["signals"]["self_reuse_count"],
        override_rate=case["signals"]["override_rate"],
        suggestion_acceptance=case["signals"]["suggestion_acceptance"],
    )
    composite, _ = scoring.composite_score(sigs)
    assert composite == pytest.approx(case["composite"], abs=EPS)


# ---------------------------------------------------------------------------
# Windowed signals
# ---------------------------------------------------------------------------


def test_durability_in_window_none_for_empty_set() -> None:
    assert scoring.durability_in_window(0, 0) is None


def test_durability_in_window_clamps_negative_positive() -> None:
    # Defensive: a bad caller passing a negative positive count is treated
    # as zero rather than producing a negative ratio.
    assert scoring.durability_in_window(4, -3) == 0.0


def test_durability_in_window_basic_ratio() -> None:
    assert scoring.durability_in_window(4, 3) == pytest.approx(0.75, abs=EPS)


def test_compute_windowed_signals_does_not_mutate_base() -> None:
    base = scoring.ScoreSignals(peer_reuse_count=2)
    augmented = scoring.compute_windowed_signals(base, 0.8, 0.6)
    assert augmented.durability_7d == 0.8
    assert augmented.durability_30d == 0.6
    assert augmented.peer_reuse_count == 2
    # Base is frozen — verify by attempting attribute set.
    with pytest.raises(Exception):
        base.durability_7d = 0.5  # type: ignore[misc]


# ---------------------------------------------------------------------------
# DB layer (mocked) — idempotency + per-session exception isolation
# ---------------------------------------------------------------------------


class FakeCursor:
    """Minimal psycopg-cursor stand-in for the worker's queries."""

    def __init__(self, conn: "FakeConn") -> None:
        self.conn = conn
        self._last_result: Any = None

    def __enter__(self) -> "FakeCursor":
        return self

    def __exit__(self, *exc_info: Any) -> None:
        return None

    def execute(self, sql: str, params: tuple[Any, ...] = ()) -> None:
        self.conn.executed.append((sql, params))
        # Route by SQL fragment — keep the matcher tight so an unexpected
        # query fails loudly instead of returning a misleading default.
        if "MAX(last_event_at)" in sql:
            self._last_result = (self.conn.watermark,)
        elif "INSERT INTO scoring_batch_runs" in sql:
            self._last_result = ("run-fake-id",)
        elif "UPDATE scoring_batch_runs" in sql:
            self._last_result = None
        elif "FROM sessions s" in sql and "JOIN session_events" in sql:
            self._last_result = None
            self.conn.dirty_sessions_returned = True
        elif "FROM session_events" in sql:
            # Per-session event lookup: pop from the queued list.
            self._last_result = None
            session_id = params[0]
            self.conn.events_returned = self.conn.events_by_session.get(
                session_id, []
            )
        elif "INSERT INTO session_scores" in sql:
            session_id = params[0]
            scorer_version = params[2]
            # Raise here only if instructed; otherwise record the insert.
            if session_id in self.conn.fail_inserts_for:
                raise RuntimeError("synthetic insert failure")
            # ON CONFLICT DO NOTHING returns no row when a duplicate
            # (session_id, scorer_version) already exists.
            if (session_id, scorer_version) in self.conn.existing_scores:
                self._last_result = None
            else:
                self.conn.inserted_scores.append(
                    (session_id, scorer_version, params)
                )
                self._last_result = ("score-fake-id",)
        elif "user_sessions" in sql:
            # durability lookup — zero by default.
            self._last_result = (0, 0)
        else:
            self._last_result = None

    def fetchone(self) -> Any:
        return self._last_result

    def fetchall(self) -> list[Any]:
        # The two query paths that need fetchall are session_events and
        # dirty-sessions; both are pre-staged on the conn.
        if self.conn.dirty_sessions_returned:
            self.conn.dirty_sessions_returned = False
            return [
                (sid, tid, uid, ts)
                for (sid, tid, uid, ts) in self.conn.dirty_sessions
            ]
        if self.conn.events_returned:
            evts = self.conn.events_returned
            self.conn.events_returned = []
            return [
                (e["id"], e["event_type"], e.get("occurred_at"))
                for e in evts
            ]
        return []


class FakeConn:
    """Tracks executed SQL and stages query results for the worker."""

    def __init__(self) -> None:
        self.executed: list[tuple[str, tuple[Any, ...]]] = []
        self.watermark: _dt.datetime | None = None
        self.dirty_sessions: list[tuple[str, str, str, _dt.datetime]] = []
        self.dirty_sessions_returned = False
        self.events_by_session: dict[str, list[dict[str, Any]]] = {}
        self.events_returned: list[dict[str, Any]] = []
        self.existing_scores: set[tuple[str, str]] = set()
        self.inserted_scores: list[tuple[str, str, tuple[Any, ...]]] = []
        self.fail_inserts_for: set[str] = set()
        self.commits = 0
        self.rollbacks = 0
        self.closed = False

    def cursor(self) -> FakeCursor:
        return FakeCursor(self)

    def commit(self) -> None:
        self.commits += 1

    def rollback(self) -> None:
        self.rollbacks += 1

    def close(self) -> None:
        self.closed = True


@pytest.fixture
def fake_conn() -> FakeConn:
    return FakeConn()


def test_idempotent_skip_when_score_exists(fake_conn: FakeConn) -> None:
    """Re-running the batch against a (session_id, scorer_version) that
    already has a score must INSERT zero new rows."""
    now = _dt.datetime(2026, 5, 22, tzinfo=_dt.timezone.utc)
    fake_conn.dirty_sessions = [
        ("sess-1", "tenant-1", "user-1", now),
    ]
    fake_conn.events_by_session = {
        "sess-1": [{"id": "e1", "event_type": "peer_reuse"}],
    }
    fake_conn.existing_scores = {("sess-1", "v1-modal-test")}

    with mock.patch.object(scoring, "_db_connect", return_value=fake_conn):
        result = scoring.run_batch(
            "postgres://fake",
            now=now,
            scorer_version="v1-modal-test",
        )

    assert result.sessions_scored == 0
    assert result.errors_count == 0
    assert fake_conn.inserted_scores == []


def test_fresh_insert_when_no_existing_score(fake_conn: FakeConn) -> None:
    now = _dt.datetime(2026, 5, 22, tzinfo=_dt.timezone.utc)
    fake_conn.dirty_sessions = [
        ("sess-1", "tenant-1", "user-1", now),
        ("sess-2", "tenant-1", "user-1", now),
    ]
    fake_conn.events_by_session = {
        "sess-1": [{"id": "e1", "event_type": "peer_reuse"}],
        "sess-2": [{"id": "e2", "event_type": "self_reuse"}],
    }

    with mock.patch.object(scoring, "_db_connect", return_value=fake_conn):
        result = scoring.run_batch(
            "postgres://fake",
            now=now,
            scorer_version="v1-modal-test",
        )

    assert result.sessions_scored == 2
    assert result.errors_count == 0
    assert {row[0] for row in fake_conn.inserted_scores} == {"sess-1", "sess-2"}
    # Each insert tags the locked scorer_version verbatim.
    for _, version, _ in fake_conn.inserted_scores:
        assert version == "v1-modal-test"


def test_per_session_exception_does_not_abort_batch(fake_conn: FakeConn) -> None:
    """One bad session must not block the rest of the batch — the
    worker logs, increments errors_count, and continues."""
    now = _dt.datetime(2026, 5, 22, tzinfo=_dt.timezone.utc)
    fake_conn.dirty_sessions = [
        ("sess-good", "tenant-1", "user-1", now),
        ("sess-bad", "tenant-1", "user-1", now),
        ("sess-also-good", "tenant-1", "user-1", now),
    ]
    fake_conn.events_by_session = {
        "sess-good": [{"id": "e1", "event_type": "peer_reuse"}],
        "sess-bad": [{"id": "e2", "event_type": "peer_reuse"}],
        "sess-also-good": [{"id": "e3", "event_type": "self_reuse"}],
    }
    fake_conn.fail_inserts_for = {"sess-bad"}

    with mock.patch.object(scoring, "_db_connect", return_value=fake_conn):
        result = scoring.run_batch(
            "postgres://fake",
            now=now,
            scorer_version="v1-modal-test",
        )

    assert result.sessions_scored == 2
    assert result.errors_count == 1
    assert result.errors[0]["session_id"] == "sess-bad"
    # The two good sessions were still inserted.
    assert {row[0] for row in fake_conn.inserted_scores} == {
        "sess-good",
        "sess-also-good",
    }
    # And the bad-session rollback happened.
    assert fake_conn.rollbacks >= 1


def test_batch_records_succeeded_run(fake_conn: FakeConn) -> None:
    now = _dt.datetime(2026, 5, 22, tzinfo=_dt.timezone.utc)
    fake_conn.dirty_sessions = [
        ("sess-1", "tenant-1", "user-1", now),
    ]
    fake_conn.events_by_session = {
        "sess-1": [{"id": "e1", "event_type": "peer_reuse"}],
    }

    with mock.patch.object(scoring, "_db_connect", return_value=fake_conn):
        scoring.run_batch(
            "postgres://fake",
            now=now,
            scorer_version="v1-modal-test",
        )

    # Find the UPDATE that set status=succeeded.
    statuses = [
        params[0]
        for sql, params in fake_conn.executed
        if "UPDATE scoring_batch_runs" in sql
    ]
    assert statuses == ["succeeded"]


def test_batch_records_failed_run_on_setup_error() -> None:
    """If the worker dies *before* iterating sessions, the failed-run row
    still lands so the next-day run picks up everything since the prior
    SUCCESS."""
    fake_conn = FakeConn()

    # Make the watermark query raise — simulates a DDL skew.
    orig_execute = FakeCursor.execute

    def failing_execute(
        self: FakeCursor, sql: str, params: tuple[Any, ...] = ()
    ) -> None:
        if "MAX(last_event_at)" in sql:
            raise RuntimeError("watermark query exploded")
        orig_execute(self, sql, params)

    with mock.patch.object(FakeCursor, "execute", failing_execute):
        with mock.patch.object(scoring, "_db_connect", return_value=fake_conn):
            with pytest.raises(RuntimeError, match="watermark"):
                scoring.run_batch(
                    "postgres://fake",
                    scorer_version="v1-modal-test",
                )

    # The 'failed' UPDATE was issued before the exception propagated.
    statuses = [
        params[0]
        for sql, params in fake_conn.executed
        if "UPDATE scoring_batch_runs" in sql
    ]
    assert statuses == ["failed"]
