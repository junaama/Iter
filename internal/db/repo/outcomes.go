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

// Outcome is the storage shape for the outcomes table. Mirrors
// migrations/0001_initial.sql column-for-column. Details is the raw
// jsonb payload (webhook senders ship vendor-specific shapes; we don't
// normalize at the storage layer).
//
// ExternalRef is nullable in the schema: not every outcome has a
// remote anchor (e.g. "tests_passed" recorded by an internal sweeper).
// When set, it's the canonical (session_id, outcome_type, external_ref)
// dedup key — see migrations/0003_score_outcome_dedup.sql.
type Outcome struct {
	ID          uuid.UUID       `db:"id"`
	SessionID   uuid.UUID       `db:"session_id"`
	TenantID    uuid.UUID       `db:"tenant_id"`
	OutcomeType string          `db:"outcome_type"`
	ExternalRef *string         `db:"external_ref"`
	Details     json.RawMessage `db:"details"`
	ObservedAt  time.Time       `db:"observed_at"`
}

// Outcome type constants mirror the CHECK constraint in
// migrations/0001_initial.sql. Kept here so callers reach for a typed
// value rather than re-typing the literal at every call site.
const (
	OutcomeCommitLanded         = "commit_landed"
	OutcomePRMerged             = "pr_merged"
	OutcomePRReverted           = "pr_reverted"
	OutcomeCodeRevertedWithin7d = "code_reverted_within_7d"
	OutcomeTestsPassed          = "tests_passed"
	OutcomeTestsFailed          = "tests_failed"
	OutcomeIncidentCaused       = "incident_caused"
	OutcomePeerReferenced       = "peer_referenced"
)

// validOutcomeTypes mirrors the migration's CHECK constraint. Client-side
// gating turns a typo into a domain error rather than a constraint
// violation surfacing as a generic SQL error.
var validOutcomeTypes = map[string]struct{}{
	OutcomeCommitLanded:         {},
	OutcomePRMerged:             {},
	OutcomePRReverted:           {},
	OutcomeCodeRevertedWithin7d: {},
	OutcomeTestsPassed:          {},
	OutcomeTestsFailed:          {},
	OutcomeIncidentCaused:       {},
	OutcomePeerReferenced:       {},
}

// ErrAlreadyExists is returned by InsertOutcome when a webhook
// delivers the same (session_id, outcome_type, external_ref) twice.
// Callers should treat it as a benign no-op — at-least-once delivery
// IS the design (ARCHITECTURE.md §7 webhook redelivery row).
var ErrAlreadyExists = errors.New("repo: row already exists")

// InsertOutcome inserts an outcomes row. When ExternalRef is set and a
// row with the same (session_id, outcome_type, external_ref) already
// exists (see migrations/0004_score_outcome_dedup.sql), returns
// ErrAlreadyExists wrapped with the standard repo error prefix.
//
// When ExternalRef is nil, no DB-level dedup applies; the caller is
// expected to dedup if their domain requires it (e.g. by inspecting
// ListOutcomesForSession before inserting).
//
// Why the ON CONFLICT dance instead of catching the unique-violation:
// a unique-violation in Postgres aborts the surrounding transaction,
// so the WithTenant-style callers couldn't continue work after a
// duplicate webhook. We let Postgres do the right thing via
// `ON CONFLICT DO NOTHING` and detect the no-op via the empty
// RETURNING clause — the tx stays usable.
func InsertOutcome(ctx context.Context, tx pgx.Tx, o Outcome) (Outcome, error) {
	if o.SessionID == uuid.Nil {
		return Outcome{}, errors.New("repo.outcomes.insert: session_id required")
	}
	if o.TenantID == uuid.Nil {
		return Outcome{}, errors.New("repo.outcomes.insert: tenant_id required")
	}
	if _, ok := validOutcomeTypes[o.OutcomeType]; !ok {
		return Outcome{}, fmt.Errorf("repo.outcomes.insert: invalid outcome_type %q", o.OutcomeType)
	}
	if len(o.Details) == 0 {
		o.Details = json.RawMessage(`{}`)
	}
	var observedArg any
	if o.ObservedAt.IsZero() {
		observedArg = nil // default to now()
	} else {
		observedArg = o.ObservedAt
	}

	var out Outcome
	err := tx.QueryRow(ctx, `
		INSERT INTO outcomes (
		  session_id, tenant_id, outcome_type, external_ref, details, observed_at
		) VALUES ($1,$2,$3,$4,$5, COALESCE($6::timestamptz, now()))
		ON CONFLICT (session_id, outcome_type, external_ref)
		  WHERE external_ref IS NOT NULL
		  DO NOTHING
		RETURNING id, session_id, tenant_id, outcome_type, external_ref, details, observed_at
	`,
		o.SessionID, o.TenantID, o.OutcomeType, o.ExternalRef,
		[]byte(o.Details), observedArg,
	).Scan(
		&out.ID, &out.SessionID, &out.TenantID, &out.OutcomeType,
		&out.ExternalRef, &out.Details, &out.ObservedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING ate the insert. Surface as
			// ErrAlreadyExists so webhook handlers can treat the
			// duplicate delivery as the benign no-op it is. The
			// tx is still usable — that's the whole reason we
			// take this path instead of catching 23505 after an
			// aborted statement.
			return Outcome{}, fmt.Errorf("repo.outcomes.insert: %w", ErrAlreadyExists)
		}
		return Outcome{}, fmt.Errorf("repo.outcomes.insert: %w", err)
	}
	return out, nil
}

// ListOutcomesForSession returns every outcome attached to sessionID
// ordered by observed_at DESC, id DESC for ties. Used by the
// session-detail page to show the downstream effect of an agent run.
//
// Disambiguated from session_scores.ListScoresForSession at the
// package-symbol level on purpose — see that file's comment.
func ListOutcomesForSession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) ([]Outcome, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, session_id, tenant_id, outcome_type, external_ref, details, observed_at
		  FROM outcomes
		 WHERE session_id = $1
		 ORDER BY observed_at DESC, id DESC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("repo.outcomes.list: %w", err)
	}
	defer rows.Close()
	return scanOutcomes(rows)
}

// CountOutcomesByTypeSince counts outcomes of outcomeType for tenantID
// whose observed_at >= since. Used by the tenant dashboard's "merged PRs
// this week" tile and similar rollups.
//
// RLS already scopes to the current tenant; the tenantID argument is
// passed to make the intent obvious and to assert at the call site
// that the surrounding WithTenant block matches.
func CountOutcomesByTypeSince(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
	outcomeType string,
	since time.Time,
) (int, error) {
	if _, ok := validOutcomeTypes[outcomeType]; !ok {
		return 0, fmt.Errorf("repo.outcomes.count: invalid outcome_type %q", outcomeType)
	}
	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM outcomes
		 WHERE tenant_id = $1
		   AND outcome_type = $2
		   AND observed_at >= $3
	`, tenantID, outcomeType, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repo.outcomes.count: %w", err)
	}
	return n, nil
}

// scanOutcomes drains a pgx.Rows that selected the canonical outcomes
// column list.
func scanOutcomes(rows pgx.Rows) ([]Outcome, error) {
	var out []Outcome
	for rows.Next() {
		var o Outcome
		if err := rows.Scan(
			&o.ID, &o.SessionID, &o.TenantID, &o.OutcomeType,
			&o.ExternalRef, &o.Details, &o.ObservedAt,
		); err != nil {
			return nil, fmt.Errorf("repo.outcomes scan: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.outcomes iter: %w", err)
	}
	return out, nil
}
