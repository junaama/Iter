package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Session mirrors the sessions table column-for-column. Nullable
// columns use pointer types so the zero value is unambiguous.
type Session struct {
	ID              uuid.UUID  `db:"id"`
	TenantID        uuid.UUID  `db:"tenant_id"`
	UserID          uuid.UUID  `db:"user_id"`
	ParentSessionID *uuid.UUID `db:"parent_session_id"`
	Harness         string     `db:"harness"`
	Model           string     `db:"model"`
	Effort          *string    `db:"effort"`
	Tools           []string   `db:"tools"`
	RepoHash        *string    `db:"repo_hash"`
	GitBranch       *string    `db:"git_branch"`
	StartedAt       time.Time  `db:"started_at"`
	EndedAt         *time.Time `db:"ended_at"`
	WallTimeMs      *int32     `db:"wall_time_ms"`
	TurnCount       *int32     `db:"turn_count"`
	TotalTokensIn   *int64     `db:"total_tokens_in"`
	TotalTokensOut  *int64     `db:"total_tokens_out"`
	RedactedPrompt  string     `db:"redacted_prompt"`
	RedactedSystem  *string    `db:"redacted_system"`
	Classification  string     `db:"classification"`
	IngestedAt      time.Time  `db:"ingested_at"`
	ArchivedAt      *time.Time `db:"archived_at"`
}

// Valid classification values mirror the CHECK constraint. Only `clean`
// and `strippable` rows ever flow through the cloud; `dirty` is local-only
// (CLAUDE.md "Three-tier redaction classification").
const (
	ClassificationClean      = "clean"
	ClassificationStrippable = "strippable"
	ClassificationDirty      = "dirty"
)

// validClassifications is the closed set Postgres will accept.
var validClassifications = map[string]struct{}{
	ClassificationClean:      {},
	ClassificationStrippable: {},
	ClassificationDirty:      {},
}

// SessionFilter narrows a ListSessions call. Empty fields are
// ignored; the filter is intentionally permissive — RLS hides the rest.
type SessionFilter struct {
	UserID  *uuid.UUID // optional: only sessions for this user
	Since   *time.Time // optional: started_at >= Since
	Until   *time.Time // optional: started_at <  Until
	Harness *string    // optional: exact-match on harness
}

// InsertSession inserts a sessions row. The caller fills in TenantID,
// UserID, redacted prompts, harness/model/classification, and any
// optional fields. ID, IngestedAt are server-assigned.
func InsertSession(ctx context.Context, tx pgx.Tx, s Session) (Session, error) {
	if s.TenantID == uuid.Nil {
		return Session{}, errors.New("repo.sessions.insert: tenant_id required")
	}
	if s.UserID == uuid.Nil {
		return Session{}, errors.New("repo.sessions.insert: user_id required")
	}
	if s.Harness == "" || s.Model == "" {
		return Session{}, errors.New("repo.sessions.insert: harness and model required")
	}
	if s.RedactedPrompt == "" {
		return Session{}, errors.New("repo.sessions.insert: redacted_prompt required")
	}
	if _, ok := validClassifications[s.Classification]; !ok {
		return Session{}, fmt.Errorf("repo.sessions.insert: invalid classification %q", s.Classification)
	}
	if s.StartedAt.IsZero() {
		return Session{}, errors.New("repo.sessions.insert: started_at required")
	}

	if s.Tools == nil {
		s.Tools = []string{}
	}

	var out Session
	err := tx.QueryRow(ctx, `
		INSERT INTO sessions (
		  tenant_id, user_id, parent_session_id, harness, model, effort,
		  tools, repo_hash, git_branch, started_at, ended_at, wall_time_ms,
		  turn_count, total_tokens_in, total_tokens_out, redacted_prompt,
		  redacted_system, classification
		) VALUES (
		  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18
		)
		RETURNING
		  id, tenant_id, user_id, parent_session_id, harness, model, effort,
		  tools, repo_hash, git_branch, started_at, ended_at, wall_time_ms,
		  turn_count, total_tokens_in, total_tokens_out, redacted_prompt,
		  redacted_system, classification, ingested_at, archived_at
	`,
		s.TenantID, s.UserID, s.ParentSessionID, s.Harness, s.Model, s.Effort,
		s.Tools, s.RepoHash, s.GitBranch, s.StartedAt, s.EndedAt, s.WallTimeMs,
		s.TurnCount, s.TotalTokensIn, s.TotalTokensOut, s.RedactedPrompt,
		s.RedactedSystem, s.Classification,
	).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.ParentSessionID, &out.Harness,
		&out.Model, &out.Effort, &out.Tools, &out.RepoHash, &out.GitBranch,
		&out.StartedAt, &out.EndedAt, &out.WallTimeMs, &out.TurnCount,
		&out.TotalTokensIn, &out.TotalTokensOut, &out.RedactedPrompt,
		&out.RedactedSystem, &out.Classification, &out.IngestedAt, &out.ArchivedAt,
	)
	if err != nil {
		return Session{}, fmt.Errorf("repo.sessions.insert: %w", err)
	}
	return out, nil
}

// GetSession returns a session by id. Returns pgx.ErrNoRows when
// missing — including when RLS hides the row from the current tenant.
// Callers cannot distinguish "doesn't exist" from "not yours" by design.
func GetSession(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Session, error) {
	var s Session
	err := tx.QueryRow(ctx, sessionSelectAllColumns+`
		  FROM sessions
		 WHERE id = $1
	`, id).Scan(scanSessionTargets(&s)...)
	if err != nil {
		return Session{}, fmt.Errorf("repo.sessions.get: %w", err)
	}
	return s, nil
}

// ListRecentByUser returns the most recent `limit` sessions for a user
// in started_at DESC order. RLS scopes by tenant; this filter narrows
// further to the user.
func ListRecentByUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := tx.Query(ctx, sessionSelectAllColumns+`
		  FROM sessions
		 WHERE user_id = $1
		 ORDER BY started_at DESC, id DESC
		 LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo.sessions.list_recent_by_user: %w", err)
	}
	defer rows.Close()
	return scanSessions(rows)
}

// FindByRepoCommit returns the most recent session whose repo_hash
// matches and which has a session_events row of type 'git_commit'
// whose payload->>'sha' equals commitSHA. Used by the GitHub webhook
// (issue 041) to map an inbound pull_request / check_run event back to
// the session that produced the commit.
//
// repoHash is the SHA-256 of the canonical repo URL (formula recorded
// in DECISIONS.md "repo_hash formula (issue 041)"). commitSHA is the
// 40-char hex git SHA.
//
// Returns pgx.ErrNoRows when nothing matches under RLS. Callers in the
// webhook path buffer the event into pending_outcomes on miss so the
// late-match sweeper can retry once the session lands.
func FindByRepoCommit(ctx context.Context, tx pgx.Tx, repoHash, commitSHA string) (Session, error) {
	if repoHash == "" || commitSHA == "" {
		return Session{}, errors.New("repo.sessions.find_by_repo_commit: repo_hash and commit_sha required")
	}
	var s Session
	err := tx.QueryRow(ctx, sessionSelectAllColumns+`
		  FROM sessions s
		 WHERE s.repo_hash = $1
		   AND EXISTS (
		     SELECT 1 FROM session_events e
		      WHERE e.session_id = s.id
		        AND e.event_type = 'git_commit'
		        AND e.payload->>'sha' = $2
		   )
		 ORDER BY s.started_at DESC
		 LIMIT 1
	`, repoHash, commitSHA).Scan(scanSessionTargets(&s)...)
	if err != nil {
		return Session{}, fmt.Errorf("repo.sessions.find_by_repo_commit: %w", err)
	}
	return s, nil
}

// FindByID fetches a session by id without applying any extra
// filtering. Identical to GetSession but exported under a name that
// reads naturally at webhook call sites where the lookup is
// marker-style ("Closes session: <uuid>" parsed from a commit message).
// Returns pgx.ErrNoRows when the row is hidden by RLS or doesn't exist.
func FindByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Session, error) {
	return GetSession(ctx, tx, id)
}

// ListSubagents returns all sessions whose parent_session_id matches
// the given id, ordered by started_at ASC for chronological replay.
func ListSubagents(ctx context.Context, tx pgx.Tx, parentID uuid.UUID) ([]Session, error) {
	rows, err := tx.Query(ctx, sessionSelectAllColumns+`
		  FROM sessions
		 WHERE parent_session_id = $1
		 ORDER BY started_at ASC, id ASC
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("repo.sessions.list_subagents: %w", err)
	}
	defer rows.Close()
	return scanSessions(rows)
}

// ListSessions executes the filter under RLS and returns up to limit
// rows ordered by (started_at DESC, id DESC). Cursor is the (started_at,
// id) tuple of the last row from the prior page; pass the zero time +
// uuid.Nil to fetch the first page.
func ListSessions(
	ctx context.Context,
	tx pgx.Tx,
	filter SessionFilter,
	limit int,
	cursorStartedAt time.Time,
	cursorID uuid.UUID,
) ([]Session, error) {
	if limit <= 0 {
		limit = 25
	}

	// Build the dynamic WHERE inline. We deliberately avoid
	// string-concat user input — every value is a placeholder.
	clauses := []string{}
	args := []any{}
	idx := 1
	addClause := func(cond string, vals ...any) {
		clauses = append(clauses, cond)
		args = append(args, vals...)
		idx += len(vals)
	}
	if filter.UserID != nil {
		addClause(fmt.Sprintf("user_id = $%d", idx), *filter.UserID)
	}
	if filter.Since != nil {
		addClause(fmt.Sprintf("started_at >= $%d", idx), *filter.Since)
	}
	if filter.Until != nil {
		addClause(fmt.Sprintf("started_at < $%d", idx), *filter.Until)
	}
	if filter.Harness != nil {
		addClause(fmt.Sprintf("harness = $%d", idx), *filter.Harness)
	}
	if cursorID != uuid.Nil {
		addClause(fmt.Sprintf("(started_at, id) < ($%d, $%d)", idx, idx+1), cursorStartedAt, cursorID)
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit)
	sql := sessionSelectAllColumns + " FROM sessions " + where + fmt.Sprintf(" ORDER BY started_at DESC, id DESC LIMIT $%d", idx)

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("repo.sessions.list: %w", err)
	}
	defer rows.Close()
	return scanSessions(rows)
}

// MarkSessionArchived stamps archived_at = at on a session. Idempotent
// once set: callers can issue this repeatedly without an error, but the
// timestamp is only set the first time.
func MarkSessionArchived(ctx context.Context, tx pgx.Tx, id uuid.UUID, at time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE sessions SET archived_at = $2
		 WHERE id = $1 AND archived_at IS NULL
	`, id, at)
	if err != nil {
		return fmt.Errorf("repo.sessions.mark_archived: %w", err)
	}
	// RowsAffected == 0 means either the row is hidden by RLS or
	// already archived; both are acceptable no-ops at this layer.
	_ = tag
	return nil
}

// DeleteSession removes a session by id. Cascades to events,
// embeddings, scores, outcomes via FK ON DELETE CASCADE.
func DeleteSession(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("repo.sessions.delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.sessions.delete: %w", pgx.ErrNoRows)
	}
	return nil
}

// sessionSelectAllColumns is the canonical column list used by every
// session SELECT. Keeping it in one place stops drift when columns are
// added — every list function picks up the new column at the same time.
const sessionSelectAllColumns = `
SELECT
  id, tenant_id, user_id, parent_session_id, harness, model, effort,
  tools, repo_hash, git_branch, started_at, ended_at, wall_time_ms,
  turn_count, total_tokens_in, total_tokens_out, redacted_prompt,
  redacted_system, classification, ingested_at, archived_at
`

// scanSessionTargets returns the slice of pointers that
// sessionSelectAllColumns scans into, in field order.
func scanSessionTargets(s *Session) []any {
	return []any{
		&s.ID, &s.TenantID, &s.UserID, &s.ParentSessionID, &s.Harness,
		&s.Model, &s.Effort, &s.Tools, &s.RepoHash, &s.GitBranch,
		&s.StartedAt, &s.EndedAt, &s.WallTimeMs, &s.TurnCount,
		&s.TotalTokensIn, &s.TotalTokensOut, &s.RedactedPrompt,
		&s.RedactedSystem, &s.Classification, &s.IngestedAt, &s.ArchivedAt,
	}
}

// scanSessions drains a pgx.Rows that selected sessionSelectAllColumns.
func scanSessions(rows pgx.Rows) ([]Session, error) {
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(scanSessionTargets(&s)...); err != nil {
			return nil, fmt.Errorf("repo.sessions scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.sessions iter: %w", err)
	}
	return out, nil
}
