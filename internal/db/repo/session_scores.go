package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Score is the storage shape for the session_scores table. Mirrors
// migrations/0001_initial.sql column-for-column. composite_score is a
// numeric(4,3) on the SQL side and arrives as float64 in Go — the
// scoring contract (pkg/contracts.CompositeScoreOutput) constrains it
// to [0.0, 1.0] and the CHECK constraint enforces the same.
//
// Signals is the raw jsonb payload (pkg/contracts.ScoreSignals
// supports extra="allow" — new signals land without a code change).
// We keep it as []byte at the repo layer so the storage layer is
// agnostic to the wire schema's evolution; the API layer
// unmarshals/marshals against contracts.ScoreSignals.
type Score struct {
	ID                uuid.UUID       `db:"id"`
	SessionID         uuid.UUID       `db:"session_id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	ScorerVersion     string          `db:"scorer_version"`
	CompositeScore    float64         `db:"composite_score"`
	Signals           json.RawMessage `db:"signals"`
	Rationale         *string         `db:"rationale"`
	ContributorWeight float64         `db:"contributor_weight"`
	ScoredAt          time.Time       `db:"scored_at"`
}

// DailyScore is the row shape MeanCompositePerDay returns. Day is the
// UTC date bucket; Mean is the per-day average composite score; Count
// is the number of scored sessions contributing to the bucket.
//
// Days with zero scored sessions are *not* returned — callers fill the
// gap on the API side to keep the storage path uncoupled from
// presentation concerns (sparkline rendering wants every day; a CSV
// export does not).
type DailyScore struct {
	Day   time.Time
	Mean  float64
	Count int
}

// InsertScore inserts a session_scores row. ON CONFLICT (session_id,
// scorer_version) DO NOTHING — the nightly scorer (ARCHITECTURE.md §9
// Step 6) MUST be idempotent because the Modal batch may retry.
//
// The returned Score is the row that ended up in the table — either
// the one we just inserted, or (on conflict) the pre-existing row.
// Callers that need to detect "was it new?" can compare Score.ID to
// uuid.Nil before/after, but most don't care.
func InsertScore(ctx context.Context, tx pgx.Tx, s Score) (Score, error) {
	if s.SessionID == uuid.Nil {
		return Score{}, errors.New("repo.session_scores.insert: session_id required")
	}
	if s.TenantID == uuid.Nil {
		return Score{}, errors.New("repo.session_scores.insert: tenant_id required")
	}
	if s.ScorerVersion == "" {
		return Score{}, errors.New("repo.session_scores.insert: scorer_version required")
	}
	if s.CompositeScore < 0 || s.CompositeScore > 1 {
		return Score{}, fmt.Errorf("repo.session_scores.insert: composite_score %.3f out of [0,1]", s.CompositeScore)
	}
	if s.ContributorWeight < 0 || s.ContributorWeight > 1 {
		return Score{}, fmt.Errorf("repo.session_scores.insert: contributor_weight %.3f out of [0,1]", s.ContributorWeight)
	}
	if len(s.Signals) == 0 {
		s.Signals = json.RawMessage(`{}`)
	}

	// `INSERT ... ON CONFLICT DO NOTHING ... RETURNING` returns zero
	// rows when the conflict fires, so we do the upsert with a
	// follow-up SELECT that always returns the canonical row. The
	// alternative (`ON CONFLICT (...) DO UPDATE SET id = id`) makes
	// every retry bump xmin and re-trigger replication; we don't
	// want that on a hot path.
	_, err := tx.Exec(ctx, `
		INSERT INTO session_scores (
		  session_id, tenant_id, scorer_version, composite_score,
		  signals, rationale, contributor_weight
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (session_id, scorer_version) DO NOTHING
	`,
		s.SessionID, s.TenantID, s.ScorerVersion, s.CompositeScore,
		[]byte(s.Signals), s.Rationale, s.ContributorWeight,
	)
	if err != nil {
		return Score{}, fmt.Errorf("repo.session_scores.insert: %w", err)
	}

	var out Score
	err = tx.QueryRow(ctx, `
		SELECT id, session_id, tenant_id, scorer_version, composite_score,
		       signals, rationale, contributor_weight, scored_at
		  FROM session_scores
		 WHERE session_id = $1 AND scorer_version = $2
	`, s.SessionID, s.ScorerVersion).Scan(
		&out.ID, &out.SessionID, &out.TenantID, &out.ScorerVersion,
		&out.CompositeScore, &out.Signals, &out.Rationale,
		&out.ContributorWeight, &out.ScoredAt,
	)
	if err != nil {
		return Score{}, fmt.Errorf("repo.session_scores.insert select: %w", err)
	}
	return out, nil
}

// LatestScoreForSession returns the most recently scored row for
// sessionID. Order is scored_at DESC, ties broken by id DESC so the
// result is deterministic across schedulers that all write within the
// same microsecond.
//
// Note the name: "ScoreFor", not "For" — outcomes.go also has a
// ListForSession-shaped function, so we disambiguate at the
// package-symbol level rather than relying on import aliasing.
func LatestScoreForSession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (Score, error) {
	var s Score
	err := tx.QueryRow(ctx, `
		SELECT id, session_id, tenant_id, scorer_version, composite_score,
		       signals, rationale, contributor_weight, scored_at
		  FROM session_scores
		 WHERE session_id = $1
		 ORDER BY scored_at DESC, id DESC
		 LIMIT 1
	`, sessionID).Scan(
		&s.ID, &s.SessionID, &s.TenantID, &s.ScorerVersion,
		&s.CompositeScore, &s.Signals, &s.Rationale,
		&s.ContributorWeight, &s.ScoredAt,
	)
	if err != nil {
		return Score{}, fmt.Errorf("repo.session_scores.latest: %w", err)
	}
	return s, nil
}

// ListScoresForSession returns the full scoring history for sessionID
// ordered by scored_at DESC. Used by the session-detail UI to render
// the rescore timeline.
func ListScoresForSession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) ([]Score, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, session_id, tenant_id, scorer_version, composite_score,
		       signals, rationale, contributor_weight, scored_at
		  FROM session_scores
		 WHERE session_id = $1
		 ORDER BY scored_at DESC, id DESC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("repo.session_scores.list: %w", err)
	}
	defer rows.Close()
	return scanScores(rows)
}

// MeanCompositeForUser returns (mean, count) for the most-recent score
// of each session owned by userID with scored_at >= since. Only the
// latest score per session contributes — re-scored sessions don't
// double-count.
//
// Returns (0, 0) when there are no scored sessions in the window.
func MeanCompositeForUser(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	since time.Time,
) (float64, int, error) {
	var mean *float64
	var count int
	err := tx.QueryRow(ctx, `
		WITH latest AS (
		  SELECT DISTINCT ON (ss.session_id)
		         ss.session_id, ss.composite_score
		    FROM session_scores ss
		    JOIN sessions s ON s.id = ss.session_id
		   WHERE s.user_id = $1
		     AND ss.scored_at >= $2
		   ORDER BY ss.session_id, ss.scored_at DESC, ss.id DESC
		)
		SELECT avg(composite_score)::float8, count(*)::int
		  FROM latest
	`, userID, since).Scan(&mean, &count)
	if err != nil {
		return 0, 0, fmt.Errorf("repo.session_scores.mean_for_user: %w", err)
	}
	if mean == nil {
		return 0, 0, nil
	}
	return *mean, count, nil
}

// MeanCompositePerDay returns the per-day mean composite score for
// userID over the last `days` days (inclusive of today, UTC). Each
// session contributes its latest score for the day. Days with zero
// scored sessions are omitted; callers fill in zeros if their
// presentation needs them.
//
// days must be >= 1; values <= 0 default to 7 (a sane sparkline
// window per DESIGN.md).
func MeanCompositePerDay(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	days int,
) ([]DailyScore, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)

	rows, err := tx.Query(ctx, `
		WITH latest AS (
		  SELECT DISTINCT ON (ss.session_id, date_trunc('day', ss.scored_at))
		         ss.session_id,
		         date_trunc('day', ss.scored_at) AS day,
		         ss.composite_score
		    FROM session_scores ss
		    JOIN sessions s ON s.id = ss.session_id
		   WHERE s.user_id = $1
		     AND ss.scored_at >= $2
		   ORDER BY ss.session_id, date_trunc('day', ss.scored_at),
		            ss.scored_at DESC, ss.id DESC
		)
		SELECT day, avg(composite_score)::float8 AS mean, count(*)::int AS n
		  FROM latest
		 GROUP BY day
		 ORDER BY day ASC
	`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("repo.session_scores.mean_per_day: %w", err)
	}
	defer rows.Close()

	var out []DailyScore
	for rows.Next() {
		var d DailyScore
		if err := rows.Scan(&d.Day, &d.Mean, &d.Count); err != nil {
			return nil, fmt.Errorf("repo.session_scores.mean_per_day scan: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.session_scores.mean_per_day iter: %w", err)
	}
	return out, nil
}

// scanScores drains a pgx.Rows that selected the canonical session_scores
// column list. Shared by ListForSession (and any future list call).
func scanScores(rows pgx.Rows) ([]Score, error) {
	var out []Score
	for rows.Next() {
		var s Score
		if err := rows.Scan(
			&s.ID, &s.SessionID, &s.TenantID, &s.ScorerVersion,
			&s.CompositeScore, &s.Signals, &s.Rationale,
			&s.ContributorWeight, &s.ScoredAt,
		); err != nil {
			return nil, fmt.Errorf("repo.session_scores scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.session_scores iter: %w", err)
	}
	return out, nil
}
