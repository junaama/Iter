package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const defaultInviteLinkTemplate = "https://iter.dev/invite?tenant_id={tenant_id}"

// TeamMemberAggregate is the storage projection for one dashboard/team
// member row. It intentionally mirrors pkg/contracts without importing the
// wire package from the repository layer.
type TeamMemberAggregate struct {
	UserID                uuid.UUID
	DisplayName           string
	SessionCount30d       int
	MeanCompositeScore30d *float64
}

// TeamPatternAggregate is the storage projection for one dashboard/team top
// pattern row.
type TeamPatternAggregate struct {
	PatternID   uuid.UUID
	Preview     string
	UsesCount   int
	TenantsUsed int
	AvgScore    float64
}

// TeamInviteSettings is the tenant-owned source of truth for whether the
// dashboard/team invite block should be returned.
type TeamInviteSettings struct {
	Enabled            bool
	InviteLinkTemplate string
}

// ListTeamMemberAggregates returns up to limit users with sessions in the
// supplied window. The query is deliberately single-shot: it derives session
// counts and latest-score means in one aggregate query so the dashboard path
// does not grow an N+1 loop as team size increases.
func ListTeamMemberAggregates(
	ctx context.Context,
	tx pgx.Tx,
	since time.Time,
	limit int,
) ([]TeamMemberAggregate, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `
		WITH latest_scores AS (
		  SELECT DISTINCT ON (ss.session_id)
		         ss.session_id, ss.composite_score
		    FROM session_scores ss
		    JOIN sessions s ON s.id = ss.session_id
		   WHERE s.started_at >= $1
		   ORDER BY ss.session_id, ss.scored_at DESC, ss.id DESC
		)
		SELECT
		  s.user_id,
		  COALESCE(max(u.display_name), 'former member') AS display_name,
		  count(s.id)::int AS session_count_30d,
		  avg(ls.composite_score)::float8 AS mean_composite_score_30d
		  FROM sessions s
		  LEFT JOIN users u
		    ON u.id = s.user_id
		   AND u.deleted_at IS NULL
		  LEFT JOIN latest_scores ls
		    ON ls.session_id = s.id
		 WHERE s.started_at >= $1
		 GROUP BY s.user_id
		 ORDER BY session_count_30d DESC,
		          mean_composite_score_30d DESC NULLS LAST,
		          display_name ASC,
		          s.user_id ASC
		 LIMIT $2
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("repo.dashboard_team.members: %w", err)
	}
	defer rows.Close()

	out := make([]TeamMemberAggregate, 0, limit)
	for rows.Next() {
		var m TeamMemberAggregate
		if err := rows.Scan(
			&m.UserID,
			&m.DisplayName,
			&m.SessionCount30d,
			&m.MeanCompositeScore30d,
		); err != nil {
			return nil, fmt.Errorf("repo.dashboard_team.members scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.dashboard_team.members iter: %w", err)
	}
	return out, nil
}

// ListTeamTopPatterns returns up to limit recently-used suggestions ordered
// by the issue-039 acceptance-weighted expression. RLS scopes suggestions to
// the current tenant; tenants_used is therefore always 1 for this endpoint.
func ListTeamTopPatterns(
	ctx context.Context,
	tx pgx.Tx,
	since time.Time,
	limit int,
) ([]TeamPatternAggregate, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := tx.Query(ctx, `
		SELECT
		  id,
		  source_prompt,
		  hit_count::int,
		  1 AS tenants_used,
		  CASE
		    WHEN hit_count > 0 THEN accept_count::float8 / hit_count::float8
		    ELSE 0::float8
		  END AS avg_score
		  FROM suggestions
		 WHERE last_used_at >= $1
		 ORDER BY (hit_count::float8 * accept_count::float8 / NULLIF(hit_count, 0)) DESC NULLS LAST,
		          last_used_at DESC NULLS LAST,
		          id DESC
		 LIMIT $2
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("repo.dashboard_team.patterns: %w", err)
	}
	defer rows.Close()

	out := make([]TeamPatternAggregate, 0, limit)
	for rows.Next() {
		var (
			p            TeamPatternAggregate
			sourcePrompt string
		)
		if err := rows.Scan(
			&p.PatternID,
			&sourcePrompt,
			&p.UsesCount,
			&p.TenantsUsed,
			&p.AvgScore,
		); err != nil {
			return nil, fmt.Errorf("repo.dashboard_team.patterns scan: %w", err)
		}
		p.Preview = promptPreview(sourcePrompt, 160)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.dashboard_team.patterns iter: %w", err)
	}
	return out, nil
}

// GetTeamInviteSettings reads the tenant_settings JSON object added in
// migration 0007. It returns pgx.ErrNoRows when the tenant is missing or
// soft-deleted.
func GetTeamInviteSettings(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (TeamInviteSettings, error) {
	var settings TeamInviteSettings
	err := tx.QueryRow(ctx, `
		SELECT
		  CASE
		    WHEN jsonb_typeof(tenant_settings->'team_invites_enabled') = 'boolean'
		    THEN (tenant_settings->>'team_invites_enabled')::boolean
		    ELSE false
		  END AS enabled,
		  COALESCE(
		    NULLIF(tenant_settings->>'invite_link_template', ''),
		    $2
		  ) AS invite_link_template
		  FROM tenants
		 WHERE id = $1
		   AND deleted_at IS NULL
	`, tenantID, defaultInviteLinkTemplate).Scan(&settings.Enabled, &settings.InviteLinkTemplate)
	if err != nil {
		return TeamInviteSettings{}, fmt.Errorf("repo.dashboard_team.invite_settings: %w", err)
	}
	settings.InviteLinkTemplate = strings.ReplaceAll(settings.InviteLinkTemplate, "{tenant_id}", tenantID.String())
	return settings, nil
}

func promptPreview(prompt string, maxRunes int) string {
	collapsed := strings.Join(strings.Fields(prompt), " ")
	if maxRunes <= 0 {
		return collapsed
	}
	runes := []rune(collapsed)
	if len(runes) <= maxRunes {
		return collapsed
	}
	return string(runes[:maxRunes])
}
