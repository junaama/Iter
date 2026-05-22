package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// DashboardPromptPreviewChars is the dashboard/me wire contract for
	// redacted prompt previews.
	DashboardPromptPreviewChars = 120
	dashboardDateLayout         = "2006-01-02"
)

// DashboardUser is the users-table projection returned by LoadDashboardMe.
type DashboardUser struct {
	ID          uuid.UUID
	DisplayName string
	Email       string
}

// DashboardTrendPoint is one UTC date bucket for a user's score trend.
type DashboardTrendPoint struct {
	Day            time.Time
	CompositeScore *float64
	SessionCount   int
}

// DashboardRecentSession is the recent-session projection for Dashboard / Me.
type DashboardRecentSession struct {
	ID                    uuid.UUID
	StartedAt             time.Time
	CompositeScore        *float64
	Harness               string
	RedactedPromptPreview string
}

// DashboardMe is the full personal dashboard projection.
type DashboardMe struct {
	User           DashboardUser
	Trend          []DashboardTrendPoint
	RecentSessions []DashboardRecentSession
}

// LoadDashboardMe reads the authenticated user's personal dashboard aggregate.
//
// The caller is responsible for supplying a tenant-scoped transaction. The
// query path intentionally stays at three database round-trips: user row,
// trend aggregate, recent sessions with a lateral latest-score lookup.
func LoadDashboardMe(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	days int,
	limit int,
	now time.Time,
) (DashboardMe, error) {
	if tx == nil {
		return DashboardMe{}, errors.New("repo.dashboard_me.load: tx required")
	}
	if userID == uuid.Nil {
		return DashboardMe{}, errors.New("repo.dashboard_me.load: user_id required")
	}
	if days <= 0 {
		days = 14
	}
	if limit <= 0 {
		limit = 10
	}
	if now.IsZero() {
		now = time.Now()
	}

	user, err := dashboardUser(ctx, tx, userID)
	if err != nil {
		return DashboardMe{}, err
	}
	trend, err := dashboardTrend(ctx, tx, userID, days, now)
	if err != nil {
		return DashboardMe{}, err
	}
	recent, err := dashboardRecentSessions(ctx, tx, userID, limit)
	if err != nil {
		return DashboardMe{}, err
	}

	return DashboardMe{
		User:           user,
		Trend:          trend,
		RecentSessions: recent,
	}, nil
}

func dashboardUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (DashboardUser, error) {
	var u DashboardUser
	err := tx.QueryRow(ctx, `
		SELECT id, display_name, email::text
		  FROM users
		 WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&u.ID, &u.DisplayName, &u.Email)
	if err != nil {
		return DashboardUser{}, fmt.Errorf("repo.dashboard_me.user: %w", err)
	}
	return u, nil
}

func dashboardTrend(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	days int,
	now time.Time,
) ([]DashboardTrendPoint, error) {
	startDay := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	endDay := startDay.AddDate(0, 0, days)

	rows, err := tx.Query(ctx, `
		WITH latest AS (
		  SELECT DISTINCT ON (ss.session_id, date_trunc('day', ss.scored_at))
		         date_trunc('day', ss.scored_at)::date AS day,
		         ss.session_id,
		         ss.composite_score::float8 AS composite_score,
		         ss.contributor_weight::float8 AS contributor_weight
		    FROM session_scores ss
		    JOIN sessions s ON s.id = ss.session_id
		   WHERE s.user_id = $1
		     AND ss.scored_at >= $2
		     AND ss.scored_at < $3
		   ORDER BY ss.session_id, date_trunc('day', ss.scored_at),
		            ss.scored_at DESC, ss.id DESC
		)
		SELECT day,
		       CASE
		         WHEN sum(contributor_weight) > 0
		           THEN (sum(composite_score * contributor_weight) / sum(contributor_weight))::float8
		         ELSE avg(composite_score)::float8
		       END AS weighted_mean,
		       count(*)::int AS session_count
		  FROM latest
		 GROUP BY day
		 ORDER BY day ASC
	`, userID, startDay, endDay)
	if err != nil {
		return nil, fmt.Errorf("repo.dashboard_me.trend: %w", err)
	}
	defer rows.Close()

	byDate := make(map[string]DashboardTrendPoint)
	for rows.Next() {
		var p DashboardTrendPoint
		if err := rows.Scan(&p.Day, &p.CompositeScore, &p.SessionCount); err != nil {
			return nil, fmt.Errorf("repo.dashboard_me.trend scan: %w", err)
		}
		p.Day = p.Day.UTC().Truncate(24 * time.Hour)
		byDate[p.Day.Format(dashboardDateLayout)] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.dashboard_me.trend iter: %w", err)
	}

	trend := make([]DashboardTrendPoint, 0, days)
	for i := 0; i < days; i++ {
		day := startDay.AddDate(0, 0, i)
		key := day.Format(dashboardDateLayout)
		if p, ok := byDate[key]; ok {
			trend = append(trend, p)
			continue
		}
		trend = append(trend, DashboardTrendPoint{Day: day})
	}
	return trend, nil
}

func dashboardRecentSessions(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	limit int,
) ([]DashboardRecentSession, error) {
	rows, err := tx.Query(ctx, `
		SELECT s.id,
		       s.started_at,
		       latest.composite_score,
		       s.harness,
		       CASE
		         WHEN char_length(s.redacted_prompt) > $2
		           THEN substring(s.redacted_prompt from 1 for $2) || '...'
		         ELSE s.redacted_prompt
		       END AS redacted_prompt_preview
		  FROM sessions s
		  LEFT JOIN LATERAL (
		    SELECT ss.composite_score::float8 AS composite_score
		      FROM session_scores ss
		     WHERE ss.session_id = s.id
		     ORDER BY ss.scored_at DESC, ss.id DESC
		     LIMIT 1
		  ) latest ON true
		 WHERE s.user_id = $1
		 ORDER BY s.started_at DESC, s.id DESC
		 LIMIT $3
	`, userID, DashboardPromptPreviewChars, limit)
	if err != nil {
		return nil, fmt.Errorf("repo.dashboard_me.recent: %w", err)
	}
	defer rows.Close()

	var out []DashboardRecentSession
	for rows.Next() {
		var s DashboardRecentSession
		if err := rows.Scan(
			&s.ID,
			&s.StartedAt,
			&s.CompositeScore,
			&s.Harness,
			&s.RedactedPromptPreview,
		); err != nil {
			return nil, fmt.Errorf("repo.dashboard_me.recent scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.dashboard_me.recent iter: %w", err)
	}
	return out, nil
}
