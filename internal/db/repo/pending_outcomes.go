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

// PendingOutcome buffers a webhook delivery that arrived before its
// target session was known to the cloud. See migration 0006 and
// ARCHITECTURE.md §9 Step 5 "Webhook edge cases."
//
// At v1 this is a NOT tenant-scoped table — at receive time we don't
// know the tenant. A future late-match sweeper joins by repo_hash to
// the sessions table and replays into outcomes, marking matched_at.
type PendingOutcome struct {
	ID         uuid.UUID       `db:"id"`
	Source     string          `db:"source"`
	DeliveryID string          `db:"delivery_id"`
	EventType  string          `db:"event_type"`
	Payload    json.RawMessage `db:"payload"`
	ReceivedAt time.Time       `db:"received_at"`
	MatchedAt  *time.Time      `db:"matched_at"`
}

// PendingSourceGitHub / PendingSourceLinear mirror the CHECK constraint
// in migration 0006. Caller code reaches for these constants rather
// than re-typing the literal so a typo becomes a compile error.
const (
	PendingSourceGitHub = "github"
	PendingSourceLinear = "linear"
)

var validPendingSources = map[string]struct{}{
	PendingSourceGitHub: {},
	PendingSourceLinear: {},
}

// InsertPending writes a single pending_outcomes row. The
// (source, delivery_id) UNIQUE constraint dedups redelivered webhooks
// at the DB level; a collision returns ErrAlreadyExists and the caller
// treats it as the benign no-op it is.
//
// Runs OUTSIDE WithTenant (the table has no tenant_id) — the caller
// passes a pgx.Tx obtained from db.WithBatch or directly from the
// pool. We use ON CONFLICT DO NOTHING + RETURNING to keep the
// surrounding tx alive on a duplicate, mirroring outcomes.InsertOutcome.
func InsertPending(ctx context.Context, tx pgx.Tx, p PendingOutcome) (PendingOutcome, error) {
	if _, ok := validPendingSources[p.Source]; !ok {
		return PendingOutcome{}, fmt.Errorf("repo.pending.insert: invalid source %q", p.Source)
	}
	if p.DeliveryID == "" {
		return PendingOutcome{}, errors.New("repo.pending.insert: delivery_id required")
	}
	if p.EventType == "" {
		return PendingOutcome{}, errors.New("repo.pending.insert: event_type required")
	}
	if len(p.Payload) == 0 {
		p.Payload = json.RawMessage(`{}`)
	}

	var out PendingOutcome
	err := tx.QueryRow(ctx, `
		INSERT INTO pending_outcomes (source, delivery_id, event_type, payload)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (source, delivery_id) DO NOTHING
		RETURNING id, source, delivery_id, event_type, payload, received_at, matched_at
	`, p.Source, p.DeliveryID, p.EventType, []byte(p.Payload)).Scan(
		&out.ID, &out.Source, &out.DeliveryID, &out.EventType,
		&out.Payload, &out.ReceivedAt, &out.MatchedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PendingOutcome{}, fmt.Errorf("repo.pending.insert: %w", ErrAlreadyExists)
		}
		return PendingOutcome{}, fmt.Errorf("repo.pending.insert: %w", err)
	}
	return out, nil
}

// ListUnmatched returns up to `limit` unmatched pending rows for source
// whose received_at >= since, oldest first. The late-match sweeper
// pages through these to retry the session lookup.
func ListUnmatched(ctx context.Context, tx pgx.Tx, source string, since time.Time, limit int) ([]PendingOutcome, error) {
	if _, ok := validPendingSources[source]; !ok {
		return nil, fmt.Errorf("repo.pending.list_unmatched: invalid source %q", source)
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := tx.Query(ctx, `
		SELECT id, source, delivery_id, event_type, payload, received_at, matched_at
		  FROM pending_outcomes
		 WHERE source = $1
		   AND matched_at IS NULL
		   AND received_at >= $2
		 ORDER BY received_at ASC, id ASC
		 LIMIT $3
	`, source, since, limit)
	if err != nil {
		return nil, fmt.Errorf("repo.pending.list_unmatched: %w", err)
	}
	defer rows.Close()
	return scanPending(rows)
}

// MarkMatched stamps matched_at = now() on the row. Called by the
// late-match sweeper after it successfully inserts the replayed
// outcome. Idempotent: once matched the row stays matched and the
// partial index drops it from the unmatched scan.
func MarkMatched(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE pending_outcomes SET matched_at = now()
		 WHERE id = $1 AND matched_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("repo.pending.mark_matched: %w", err)
	}
	_ = tag
	return nil
}

// DeleteOlderThan removes pending rows received before cutoff. v1
// retention story is "drop after 7d" but the sweeper job is not built
// yet (TODO documented in DECISIONS.md); this helper is here so the
// job has a single call site when it lands. Returns the number of rows
// deleted so the job can emit a metric.
func DeleteOlderThan(ctx context.Context, tx pgx.Tx, cutoff time.Time) (int64, error) {
	tag, err := tx.Exec(ctx, `
		DELETE FROM pending_outcomes WHERE received_at < $1
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("repo.pending.delete_old: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanPending(rows pgx.Rows) ([]PendingOutcome, error) {
	var out []PendingOutcome
	for rows.Next() {
		var p PendingOutcome
		if err := rows.Scan(
			&p.ID, &p.Source, &p.DeliveryID, &p.EventType,
			&p.Payload, &p.ReceivedAt, &p.MatchedAt,
		); err != nil {
			return nil, fmt.Errorf("repo.pending scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.pending iter: %w", err)
	}
	return out, nil
}
