"""Modal nightly composite-scoring batch (issue 046).

Replaces the no-op stub shipped in issue 057. Runs at 02:00 UTC daily,
pulls every session with new `session_events` since the last successful
run watermark, aggregates per-session signals, computes the locked v1
composite (DECISIONS.md "Composite scoring formula (v1)"), augments with
the *windowed* signals (`durability_7d`, `durability_30d`) that are the
nightly batch's unique contribution, and writes an idempotent
`session_scores` row per session.

Key invariants (mirror Go's ``internal/scoring`` + ``internal/signals``):

* Aggregator is pure, order-independent, dedups by event id.
* Composite is a weighted average over the subset of signals present,
  weights renormalized; output clamped to ``[0, 1]``.
* Reuse counts map to ``[0, 1)`` via ``1 - exp(-n/3)``.
* ``override_rate`` contributes ``1 - rate`` (inverted).
* NaN / negative floats are treated as missing / clamped.

DB driver: ``psycopg[binary]`` 3.x (synchronous). Picked over asyncpg
because the nightly batch has no concurrency requirement, psycopg3 has
first-class server-side `COPY` + dict-row support, and the dep graph is
smaller. Documented in DECISIONS.md.

DATABASE_URL_BATCH points at the BYPASSRLS ``iter_batch`` role so the
batch sees every tenant in one pass without `SET LOCAL
app.current_tenant`. Production wiring lives in ``deploy.md``.
"""

from __future__ import annotations

import json
import logging
import math
import os
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Mapping, Sequence

import modal

# ---------------------------------------------------------------------------
# Module constants
# ---------------------------------------------------------------------------

APP_NAME = "iter-scoring"

# Locked weights — DECISIONS.md "Composite scoring formula (v1)".
# Mirror of ``internal/scoring/scoring.go`` constants. If the Go side ever
# bumps these, bump here in the same commit (and rev SCORER_VERSION).
W_DURABILITY_7D = 0.25
W_DURABILITY_30D = 0.15
W_PEER_REUSE = 0.20
W_SELF_REUSE = 0.10
W_OVERRIDE_RATE = 0.10
W_SUGGESTION_ACCEPTANCE = 0.20

# Saturation constants for reuse counts: ``1 - exp(-n/k)``.
PEER_REUSE_SAT = 3.0
SELF_REUSE_SAT = 3.0

# Windowed signal lookback windows. Driven from the signal name (the
# nightly batch is the *only* place these are computed — see DECISIONS.md
# "Windowed signals owner: Modal").
WINDOW_7D = timedelta(days=7)
WINDOW_30D = timedelta(days=30)

# Event-type tokens — must match ``pkg/contracts/events.go`` and the
# CHECK constraint in ``migrations/0001_initial.sql``.
EVT_PEER_REUSE = "peer_reuse"
EVT_SELF_REUSE = "self_reuse"
EVT_TURN_COMPLETED = "turn_completed"
EVT_USER_OVERRIDE = "user_override"
EVT_SUGGESTION_ACCEPTED = "suggestion_accepted"
EVT_SUGGESTION_REJECTED = "suggestion_rejected"


def _scorer_version() -> str:
    """Derive the scorer_version tag for this deploy.

    Format: ``v1-modal-<sha>`` where ``<sha>`` comes from one of
    (in priority order) ``MODAL_DEPLOY_SHA``, ``GIT_SHA``, ``RAILWAY_GIT_COMMIT_SHA``.
    Falls back to ``v1-modal-dev`` for unconfigured local runs. The same
    ``session_id, scorer_version`` pair is the dedup key for
    ``session_scores ON CONFLICT DO NOTHING``, so re-running this batch
    after a re-deploy with the same sha is a no-op.
    """
    for envvar in ("MODAL_DEPLOY_SHA", "GIT_SHA", "RAILWAY_GIT_COMMIT_SHA"):
        v = os.environ.get(envvar)
        if v:
            return f"v1-modal-{v.strip()[:12]}"
    return "v1-modal-dev"


# Read at import time so tests can shadow via env or by monkey-patching
# ``scoring.SCORER_VERSION`` directly.
SCORER_VERSION = _scorer_version()

# ---------------------------------------------------------------------------
# Pure dataclasses (mirror pkg/contracts ScoreSignals shape)
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class ScoreSignals:
    """Python projection of ``pkg/contracts.ScoreSignals``.

    ``None`` means "missing signal" — present-but-zero would be
    indistinguishable from a real observation in downstream aggregations.
    """

    durability_7d: float | None = None
    durability_30d: float | None = None
    peer_reuse_count: int | None = None
    self_reuse_count: int | None = None
    override_rate: float | None = None
    suggestion_acceptance: float | None = None

    def to_dict(self) -> dict[str, float | int | None]:
        """JSON-friendly representation. Keys match contracts.py / DB jsonb."""
        return {
            "durability_7d": self.durability_7d,
            "durability_30d": self.durability_30d,
            "peer_reuse_count": self.peer_reuse_count,
            "self_reuse_count": self.self_reuse_count,
            "override_rate": self.override_rate,
            "suggestion_acceptance": self.suggestion_acceptance,
        }


@dataclass(frozen=True)
class ScoredSession:
    """Result of scoring a single session — what gets INSERTed."""

    session_id: str
    tenant_id: str
    composite_score: float
    signals: ScoreSignals
    rationale: str


@dataclass
class BatchResult:
    """Summary returned to Modal's invoker. Not pure — mutated as the
    run progresses. Frozen would force a copy per session and add noise."""

    scorer_version: str
    sessions_scored: int = 0
    errors_count: int = 0
    last_event_at: datetime | None = None
    errors: list[dict[str, str]] = field(default_factory=list)

    def to_summary(self) -> dict[str, Any]:
        return {
            "scorer_version": self.scorer_version,
            "sessions_scored": self.sessions_scored,
            "errors_count": self.errors_count,
            "last_event_at": (
                self.last_event_at.isoformat() if self.last_event_at else None
            ),
            "errors": self.errors[:50],  # cap so jsonb stays bounded
        }


# ---------------------------------------------------------------------------
# Pure helpers — port of internal/scoring and internal/signals
# ---------------------------------------------------------------------------


def _clamp01(v: float) -> float:
    if v < 0.0:
        return 0.0
    if v > 1.0:
        return 1.0
    return v


def _normalize_unit(v: float | None) -> tuple[float, bool]:
    """Return ``(value, present)``. NaN is treated as missing."""
    if v is None:
        return 0.0, False
    if math.isnan(v):
        return 0.0, False
    return _clamp01(float(v)), True


def _saturate(x: float, k: float) -> float:
    """Map non-negative count to [0,1) via ``1 - exp(-x/k)``."""
    if x <= 0:
        return 0.0
    return 1.0 - math.exp(-x / k)


def _non_negative_int(n: int | None) -> float:
    if n is None or n < 0:
        return 0.0
    return float(n)


def aggregate_signals(events: Sequence[Mapping[str, Any]]) -> ScoreSignals:
    """Port of ``internal/signals.Aggregate``.

    Input: a sequence of dicts (one per session_event row). Each dict
    MUST have ``id`` (str, may be empty) and ``event_type`` (str). Other
    fields are ignored.

    Output: a ``ScoreSignals`` with per-session signals. The windowed
    ``durability_*`` signals are NOT populated here — they require
    cross-session lookups and are added by ``compute_windowed_signals``.

    Invariants (mirrored from the Go reference):

    * Pure: no I/O, no clocks, no globals.
    * Order-independent: dedup-by-id happens before counting.
    * Empty id is NOT a dedup key — daemon WAL events arrive id-less.
    """
    if not events:
        return ScoreSignals()

    seen: set[str] = set()
    peer = 0
    self_r = 0
    turns = 0
    overrides = 0
    acceptances = 0
    rejections = 0

    for e in events:
        eid = e.get("id") or ""
        if eid:
            if eid in seen:
                continue
            seen.add(eid)

        et = e.get("event_type")
        if et == EVT_PEER_REUSE:
            peer += 1
        elif et == EVT_SELF_REUSE:
            self_r += 1
        elif et == EVT_TURN_COMPLETED:
            turns += 1
        elif et == EVT_USER_OVERRIDE:
            overrides += 1
        elif et == EVT_SUGGESTION_ACCEPTED:
            acceptances += 1
        elif et == EVT_SUGGESTION_REJECTED:
            rejections += 1

    peer_count = peer if peer > 0 else None
    self_count = self_r if self_r > 0 else None

    override_rate: float | None = None
    if overrides > 0 and turns > 0:
        override_rate = min(overrides / turns, 1.0)

    suggestion_acceptance: float | None = None
    if acceptances + rejections > 0:
        suggestion_acceptance = acceptances / (acceptances + rejections)

    return ScoreSignals(
        peer_reuse_count=peer_count,
        self_reuse_count=self_count,
        override_rate=override_rate,
        suggestion_acceptance=suggestion_acceptance,
    )


def composite_score(signals: ScoreSignals) -> tuple[float, str]:
    """Port of ``internal/scoring.Composite``.

    Returns ``(composite, rationale)``. The composite is in [0, 1];
    rationale lists which signals contributed.
    """
    num = 0.0
    den = 0.0
    contributing: list[str] = []

    v, ok = _normalize_unit(signals.durability_7d)
    if ok:
        num += W_DURABILITY_7D * v
        den += W_DURABILITY_7D
        contributing.append("durability_7d")

    v, ok = _normalize_unit(signals.durability_30d)
    if ok:
        num += W_DURABILITY_30D * v
        den += W_DURABILITY_30D
        contributing.append("durability_30d")

    # peer/self reuse counts always contribute (negative clamps to 0).
    peer = _saturate(_non_negative_int(signals.peer_reuse_count), PEER_REUSE_SAT)
    num += W_PEER_REUSE * peer
    den += W_PEER_REUSE
    contributing.append("peer_reuse_count")

    self_v = _saturate(_non_negative_int(signals.self_reuse_count), SELF_REUSE_SAT)
    num += W_SELF_REUSE * self_v
    den += W_SELF_REUSE
    contributing.append("self_reuse_count")

    v, ok = _normalize_unit(signals.override_rate)
    if ok:
        # Inverted: low override = good = high contribution.
        num += W_OVERRIDE_RATE * (1.0 - v)
        den += W_OVERRIDE_RATE
        contributing.append("override_rate")

    v, ok = _normalize_unit(signals.suggestion_acceptance)
    if ok:
        num += W_SUGGESTION_ACCEPTANCE * v
        den += W_SUGGESTION_ACCEPTANCE
        contributing.append("suggestion_acceptance")

    composite = num / den if den > 0 else 0.0
    composite = _clamp01(composite)

    rationale = "composite={:.4f} over {} signal(s): {}".format(
        composite, len(contributing), ", ".join(contributing)
    )
    return composite, rationale


# ---------------------------------------------------------------------------
# Windowed signals — the Modal batch's unique contribution
# ---------------------------------------------------------------------------


def compute_windowed_signals(
    base: ScoreSignals,
    durability_7d: float | None,
    durability_30d: float | None,
) -> ScoreSignals:
    """Return a new ScoreSignals augmenting ``base`` with windowed values.

    Pure: builds a new frozen dataclass; never mutates ``base``.

    Windowed signals are computed by the caller (typically by querying
    the same user's other sessions in a 7d/30d window and looking at
    their ratio of merged-without-revert outcomes). Encapsulated here so
    the windowed lookups can be unit-tested independently of the SQL.
    """
    return ScoreSignals(
        durability_7d=durability_7d,
        durability_30d=durability_30d,
        peer_reuse_count=base.peer_reuse_count,
        self_reuse_count=base.self_reuse_count,
        override_rate=base.override_rate,
        suggestion_acceptance=base.suggestion_acceptance,
    )


def durability_in_window(
    outcomes_in_window: int,
    positive_outcomes_in_window: int,
) -> float | None:
    """Compute a durability ratio over a windowed outcome set.

    Returns ``None`` when the denominator is zero (no signal) — mirrors
    the "counters of zero surface as None" invariant. Otherwise
    ``positive / total``, clamped to ``[0, 1]``.
    """
    if outcomes_in_window <= 0:
        return None
    if positive_outcomes_in_window < 0:
        positive_outcomes_in_window = 0
    rate = positive_outcomes_in_window / outcomes_in_window
    return _clamp01(rate)


# ---------------------------------------------------------------------------
# Database layer (psycopg3, sync)
# ---------------------------------------------------------------------------
#
# Imported lazily so ``modal/scoring_test.py`` can exercise the pure
# layer without psycopg installed locally. Tests that mock the DB layer
# never touch ``_db_connect``.


def _db_connect(database_url: str):  # type: ignore[no-untyped-def]
    """Open a psycopg3 connection. Imported lazily."""
    import psycopg  # noqa: WPS433 (lazy)

    return psycopg.connect(database_url, autocommit=False)


def _select_last_watermark(conn) -> datetime | None:  # type: ignore[no-untyped-def]
    """Return the max(last_event_at) across all succeeded runs, or None."""
    with conn.cursor() as cur:
        cur.execute(
            "SELECT MAX(last_event_at) FROM scoring_batch_runs"
            " WHERE status = 'succeeded'"
        )
        row = cur.fetchone()
    if row is None or row[0] is None:
        return None
    return row[0]


def _insert_run_started(conn, scorer_version: str) -> str:  # type: ignore[no-untyped-def]
    with conn.cursor() as cur:
        cur.execute(
            "INSERT INTO scoring_batch_runs (scorer_version, status)"
            " VALUES (%s, 'running') RETURNING id",
            (scorer_version,),
        )
        row = cur.fetchone()
    conn.commit()
    return str(row[0])


def _finish_run(
    conn,  # type: ignore[no-untyped-def]
    run_id: str,
    status: str,
    summary: dict[str, Any],
    last_event_at: datetime | None,
    sessions_scored: int,
    errors_count: int,
) -> None:
    with conn.cursor() as cur:
        cur.execute(
            "UPDATE scoring_batch_runs"
            " SET finished_at = now(),"
            "     status = %s,"
            "     sessions_scored = %s,"
            "     errors_count = %s,"
            "     last_event_at = %s,"
            "     details = %s::jsonb"
            " WHERE id = %s",
            (
                status,
                sessions_scored,
                errors_count,
                last_event_at,
                json.dumps(summary, default=str),
                run_id,
            ),
        )
    conn.commit()


def _select_dirty_sessions(  # type: ignore[no-untyped-def]
    conn, watermark: datetime | None
) -> list[tuple[str, str, str, datetime]]:
    """Return ``(session_id, tenant_id, user_id, max_occurred_at)`` for
    every session with a session_events row newer than the watermark."""
    sql = (
        "SELECT s.id::text, s.tenant_id::text, s.user_id::text,"
        "       MAX(e.occurred_at)"
        "  FROM sessions s"
        "  JOIN session_events e ON e.session_id = s.id"
        " WHERE %s::timestamptz IS NULL OR e.occurred_at > %s"
        " GROUP BY s.id"
    )
    with conn.cursor() as cur:
        cur.execute(sql, (watermark, watermark))
        return [(r[0], r[1], r[2], r[3]) for r in cur.fetchall()]


def _select_session_events(  # type: ignore[no-untyped-def]
    conn, session_id: str
) -> list[dict[str, Any]]:
    with conn.cursor() as cur:
        cur.execute(
            "SELECT id::text, event_type, occurred_at"
            "  FROM session_events"
            " WHERE session_id = %s",
            (session_id,),
        )
        return [
            {"id": r[0], "event_type": r[1], "occurred_at": r[2]}
            for r in cur.fetchall()
        ]


def _select_durability(  # type: ignore[no-untyped-def]
    conn, user_id: str, now: datetime, window: timedelta
) -> float | None:
    """Compute durability over a window for a user.

    Definition (v1): of all the user's sessions ended in the window,
    what fraction had a successful `pr_merged` outcome and NO matching
    `pr_reverted` / `code_reverted_within_7d`? This is a placeholder
    durability metric — DECISIONS.md notes the per-session formula is
    locked but the *definition* of durability is up to the nightly
    batch. Encapsulated behind a function so it can be tuned later
    without rewriting the worker.
    """
    cutoff = now - window
    sql = (
        "WITH user_sessions AS ("
        "  SELECT id FROM sessions"
        "   WHERE user_id = %s AND ended_at >= %s"
        "),"
        " positives AS ("
        "  SELECT DISTINCT o.session_id"
        "    FROM outcomes o"
        "    JOIN user_sessions us ON us.id = o.session_id"
        "   WHERE o.outcome_type = 'pr_merged'"
        "     AND NOT EXISTS ("
        "       SELECT 1 FROM outcomes o2"
        "        WHERE o2.session_id = o.session_id"
        "          AND o2.outcome_type IN ('pr_reverted','code_reverted_within_7d')"
        "     )"
        ")"
        "SELECT (SELECT count(*) FROM user_sessions),"
        "       (SELECT count(*) FROM positives)"
    )
    with conn.cursor() as cur:
        cur.execute(sql, (user_id, cutoff))
        row = cur.fetchone()
    if row is None:
        return None
    total, positive = int(row[0] or 0), int(row[1] or 0)
    return durability_in_window(total, positive)


def _insert_score(  # type: ignore[no-untyped-def]
    conn,
    scored: ScoredSession,
    scorer_version: str,
) -> bool:
    """Idempotent INSERT. Returns True when a new row was written."""
    sql = (
        "INSERT INTO session_scores"
        "   (session_id, tenant_id, scorer_version, composite_score, signals, rationale)"
        " VALUES (%s, %s, %s, %s, %s::jsonb, %s)"
        " ON CONFLICT (session_id, scorer_version) DO NOTHING"
        " RETURNING id"
    )
    with conn.cursor() as cur:
        cur.execute(
            sql,
            (
                scored.session_id,
                scored.tenant_id,
                scorer_version,
                round(scored.composite_score, 3),
                json.dumps(scored.signals.to_dict()),
                scored.rationale,
            ),
        )
        row = cur.fetchone()
    conn.commit()
    return row is not None


# ---------------------------------------------------------------------------
# Batch driver — wired to Modal
# ---------------------------------------------------------------------------
#
# Idempotency: ``session_scores`` carries a UNIQUE(session_id,
# scorer_version) constraint (migration 0004_score_outcome_dedup.sql,
# issue 052). ``_insert_score`` uses a true ON CONFLICT (...) DO NOTHING;
# re-running the batch against the same scorer_version is a no-op at
# the database level.


def _score_session(
    conn,  # type: ignore[no-untyped-def]
    session_id: str,
    tenant_id: str,
    user_id: str,
    now: datetime,
    scorer_version: str,
    logger: logging.Logger,
) -> bool:
    """Score one session. Returns True on a fresh insert, False on a
    no-op (the (session_id, scorer_version) row already exists)."""
    events = _select_session_events(conn, session_id)
    base = aggregate_signals(events)

    durability_7d = _select_durability(conn, user_id, now, WINDOW_7D)
    durability_30d = _select_durability(conn, user_id, now, WINDOW_30D)
    signals = compute_windowed_signals(base, durability_7d, durability_30d)

    composite, rationale = composite_score(signals)
    scored = ScoredSession(
        session_id=session_id,
        tenant_id=tenant_id,
        composite_score=composite,
        signals=signals,
        rationale=rationale,
    )

    inserted = _insert_score(conn, scored, scorer_version)
    if inserted:
        logger.info(
            "scored session=%s composite=%.3f version=%s",
            session_id,
            composite,
            scorer_version,
        )
    else:
        logger.debug(
            "skip %s (ON CONFLICT — already scored at %s)",
            session_id,
            scorer_version,
        )
    return inserted


def run_batch(
    database_url: str,
    *,
    now: datetime | None = None,
    scorer_version: str | None = None,
    logger: logging.Logger | None = None,
) -> BatchResult:
    """End-to-end batch driver.

    Connects to ``database_url`` (expected to be the iter_batch
    BYPASSRLS DSN), records a 'running' row in ``scoring_batch_runs``,
    iterates the dirty-sessions cursor, and finalizes the run. On any
    per-session exception: log, count, continue. On batch-level
    exception: best-effort mark the run 'failed' and re-raise so Modal
    surfaces it.
    """
    logger = logger or logging.getLogger(__name__)
    scorer_version = scorer_version or SCORER_VERSION
    now = now or datetime.now(timezone.utc)

    result = BatchResult(scorer_version=scorer_version)
    conn = _db_connect(database_url)
    run_id: str | None = None
    try:
        run_id = _insert_run_started(conn, scorer_version)
        watermark = _select_last_watermark(conn)
        sessions = _select_dirty_sessions(conn, watermark)
        logger.info(
            "scoring batch starting: %d sessions since watermark=%s",
            len(sessions),
            watermark,
        )

        for session_id, tenant_id, user_id, max_occurred in sessions:
            try:
                wrote = _score_session(
                    conn,
                    session_id,
                    tenant_id,
                    user_id,
                    now,
                    scorer_version,
                    logger,
                )
                if wrote:
                    result.sessions_scored += 1
                if (
                    result.last_event_at is None
                    or max_occurred > result.last_event_at
                ):
                    result.last_event_at = max_occurred
            except Exception as exc:  # noqa: BLE001 — must not abort the run
                logger.exception("session %s failed: %s", session_id, exc)
                result.errors_count += 1
                result.errors.append(
                    {"session_id": session_id, "error": str(exc)[:500]}
                )
                # Roll back the failed session's transaction state so
                # the next session has a clean slate.
                try:
                    conn.rollback()
                except Exception:  # noqa: BLE001
                    pass

        _finish_run(
            conn,
            run_id,
            "succeeded",
            result.to_summary(),
            result.last_event_at,
            result.sessions_scored,
            result.errors_count,
        )
        return result
    except Exception:
        # Best-effort mark failed before re-raising. We swallow the
        # secondary error so the original exception surfaces.
        if run_id is not None:
            try:
                conn.rollback()
                _finish_run(
                    conn,
                    run_id,
                    "failed",
                    result.to_summary(),
                    result.last_event_at,
                    result.sessions_scored,
                    result.errors_count,
                )
            except Exception:  # noqa: BLE001
                logger.exception("failed to record failed-run row")
        raise
    finally:
        try:
            conn.close()
        except Exception:  # noqa: BLE001
            pass


# ---------------------------------------------------------------------------
# Modal wiring
# ---------------------------------------------------------------------------

image = (
    modal.Image.debian_slim(python_version="3.12")
    .pip_install_from_requirements("requirements.txt")
)

app = modal.App(name=APP_NAME, image=image)


@app.function(
    secrets=[modal.Secret.from_name("iter-postgres")],
    schedule=modal.Cron("0 2 * * *"),
    timeout=3600,
    min_containers=2,
)
def nightly_score() -> dict[str, Any]:
    """Cron entry. Reads DATABASE_URL_BATCH from the Modal Secret."""
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    logger = logging.getLogger("iter.scoring")
    url = os.environ["DATABASE_URL_BATCH"]
    result = run_batch(url, logger=logger)
    return result.to_summary()


@app.local_entrypoint()
def main() -> None:
    """``modal run modal/scoring.py`` runs against the deployed Secret."""
    res = nightly_score.remote()
    print(json.dumps(res, indent=2, default=str))
