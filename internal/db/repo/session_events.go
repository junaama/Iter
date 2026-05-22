package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/pkg/contracts"
)

// SessionEventRow is the storage shape for session_events. id is
// bigserial (Postgres) — represented as int64 in Go. Payload is jsonb
// in the wire shape (map[string]any) to match pkg/contracts.SessionEvent.
type SessionEventRow struct {
	ID         int64               `db:"id"`
	EventID    *uuid.UUID          `db:"event_id"`
	SessionID  uuid.UUID           `db:"session_id"`
	TenantID   uuid.UUID           `db:"tenant_id"`
	EventType  contracts.EventType `db:"event_type"`
	Payload    map[string]any      `db:"payload"`
	OccurredAt time.Time           `db:"occurred_at"`
}

// SessionEventCursor is the stable pagination tuple for events ordered by
// (occurred_at ASC, id ASC).
type SessionEventCursor struct {
	OccurredAt time.Time
	ID         int64
}

// validEventTypes mirrors the migration's CHECK constraint. Client-side
// gating turns a typo into a domain error rather than a constraint
// violation. Kept in sync with pkg/contracts.EventType values — the
// constants are the source of truth.
var validEventTypes = map[contracts.EventType]struct{}{
	contracts.EventPromptSent:         {},
	contracts.EventToolCall:           {},
	contracts.EventSubagentSpawned:    {},
	contracts.EventTurnCompleted:      {},
	contracts.EventSessionCompleted:   {},
	contracts.EventUserOverride:       {},
	contracts.EventGitCommit:          {},
	contracts.EventGitRevert:          {},
	contracts.EventPROpened:           {},
	contracts.EventPRMerged:           {},
	contracts.EventPRReverted:         {},
	contracts.EventIncidentLinked:     {},
	contracts.EventPeerReuse:          {},
	contracts.EventSelfReuse:          {},
	contracts.EventSuggestionAccepted: {},
	contracts.EventSuggestionRejected: {},
}

// AppendSessionEvent inserts a single session_events row. The table is
// append-only; this is the only mutation function exposed. tenant_id
// must match the surrounding WithTenant context — the database's RLS
// policy rejects the row otherwise, surfacing as a not-null /
// permission error.
func AppendSessionEvent(ctx context.Context, tx pgx.Tx, ev SessionEventRow) (SessionEventRow, error) {
	if ev.SessionID == uuid.Nil {
		return SessionEventRow{}, errors.New("repo.session_events.append: session_id required")
	}
	if ev.TenantID == uuid.Nil {
		return SessionEventRow{}, errors.New("repo.session_events.append: tenant_id required")
	}
	if _, ok := validEventTypes[ev.EventType]; !ok {
		return SessionEventRow{}, fmt.Errorf("repo.session_events.append: invalid event_type %q", ev.EventType)
	}
	if ev.Payload == nil {
		ev.Payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		return SessionEventRow{}, fmt.Errorf("repo.session_events.append: marshal payload: %w", err)
	}

	var occurredArg any
	if ev.OccurredAt.IsZero() {
		occurredArg = nil // default to now() in SQL
	} else {
		occurredArg = ev.OccurredAt
	}

	var out SessionEventRow
	err = tx.QueryRow(ctx, `
		INSERT INTO session_events (tenant_id, session_id, event_id, event_type, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6::timestamptz, now()))
		RETURNING id, event_id, session_id, tenant_id, event_type, payload, occurred_at
	`, ev.TenantID, ev.SessionID, ev.EventID, string(ev.EventType), payloadJSON, occurredArg).Scan(
		&out.ID, &out.EventID, &out.SessionID, &out.TenantID, &out.EventType, &out.Payload, &out.OccurredAt,
	)
	if err != nil {
		return SessionEventRow{}, fmt.Errorf("repo.session_events.append: %w", err)
	}
	return out, nil
}

// UpsertSessionEvent inserts a daemon-originated event keyed by
// (tenant_id, session_id, event_id). Replayed Redis Stream deliveries return
// the existing row instead of appending duplicates.
func UpsertSessionEvent(ctx context.Context, tx pgx.Tx, ev SessionEventRow) (SessionEventRow, bool, error) {
	if ev.EventID == nil || *ev.EventID == uuid.Nil {
		return SessionEventRow{}, false, errors.New("repo.session_events.upsert: event_id required")
	}
	if ev.SessionID == uuid.Nil {
		return SessionEventRow{}, false, errors.New("repo.session_events.upsert: session_id required")
	}
	if ev.TenantID == uuid.Nil {
		return SessionEventRow{}, false, errors.New("repo.session_events.upsert: tenant_id required")
	}
	if _, ok := validEventTypes[ev.EventType]; !ok {
		return SessionEventRow{}, false, fmt.Errorf("repo.session_events.upsert: invalid event_type %q", ev.EventType)
	}
	if ev.Payload == nil {
		ev.Payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		return SessionEventRow{}, false, fmt.Errorf("repo.session_events.upsert: marshal payload: %w", err)
	}

	var occurredArg any
	if ev.OccurredAt.IsZero() {
		occurredArg = nil
	} else {
		occurredArg = ev.OccurredAt
	}

	var out SessionEventRow
	var inserted bool
	err = tx.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO session_events (tenant_id, session_id, event_id, event_type, payload, occurred_at)
			VALUES ($1, $2, $3, $4, $5, COALESCE($6::timestamptz, now()))
			ON CONFLICT (tenant_id, session_id, event_id) WHERE event_id IS NOT NULL
			DO NOTHING
			RETURNING id, event_id, session_id, tenant_id, event_type, payload, occurred_at, true AS inserted
		)
		SELECT id, event_id, session_id, tenant_id, event_type, payload, occurred_at, inserted FROM ins
		UNION ALL
		SELECT id, event_id, session_id, tenant_id, event_type, payload, occurred_at, false AS inserted
		  FROM session_events
		 WHERE tenant_id = $1 AND session_id = $2 AND event_id = $3
		   AND NOT EXISTS (SELECT 1 FROM ins)
	`, ev.TenantID, ev.SessionID, *ev.EventID, string(ev.EventType), payloadJSON, occurredArg).Scan(
		&out.ID, &out.EventID, &out.SessionID, &out.TenantID, &out.EventType, &out.Payload, &out.OccurredAt, &inserted,
	)
	if err != nil {
		return SessionEventRow{}, false, fmt.Errorf("repo.session_events.upsert: %w", err)
	}
	return out, inserted, nil
}

// ListSessionEvents returns every event for sessionID ordered by
// occurred_at ASC, id ASC for stable replay even when events share an
// occurred_at to the microsecond.
func ListSessionEvents(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) ([]SessionEventRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, event_id, session_id, tenant_id, event_type, payload, occurred_at
		  FROM session_events
		 WHERE session_id = $1
		 ORDER BY occurred_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("repo.session_events.list: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// ListSessionEventsPage returns up to limit events after cursor ordered by
// occurred_at ASC, id ASC. Passing nil cursor returns the first page.
func ListSessionEventsPage(
	ctx context.Context,
	tx pgx.Tx,
	sessionID uuid.UUID,
	limit int,
	cursor *SessionEventCursor,
) ([]SessionEventRow, error) {
	if limit <= 0 {
		limit = 10000
	}

	var (
		rows pgx.Rows
		err  error
	)
	if cursor == nil {
		rows, err = tx.Query(ctx, `
			SELECT id, event_id, session_id, tenant_id, event_type, payload, occurred_at
			  FROM session_events
			 WHERE session_id = $1
			 ORDER BY occurred_at ASC, id ASC
			 LIMIT $2
		`, sessionID, limit)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT id, event_id, session_id, tenant_id, event_type, payload, occurred_at
			  FROM session_events
			 WHERE session_id = $1
			   AND (occurred_at, id) > ($2, $3)
			 ORDER BY occurred_at ASC, id ASC
			 LIMIT $4
		`, sessionID, cursor.OccurredAt, cursor.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("repo.session_events.list_page: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// CountSessionEventsByTypeSince returns the count of events of evType
// for sessionID whose occurred_at >= since. Used by the signal
// aggregator's incremental window queries (issue 011).
func CountSessionEventsByTypeSince(
	ctx context.Context,
	tx pgx.Tx,
	sessionID uuid.UUID,
	evType contracts.EventType,
	since time.Time,
) (int64, error) {
	if _, ok := validEventTypes[evType]; !ok {
		return 0, fmt.Errorf("repo.session_events.count: invalid event_type %q", evType)
	}
	var n int64
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM session_events
		 WHERE session_id = $1
		   AND event_type = $2
		   AND occurred_at >= $3
	`, sessionID, string(evType), since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repo.session_events.count: %w", err)
	}
	return n, nil
}

// ListSessionEventsByTypeSince is the row-returning cousin of
// CountSessionEventsByTypeSince — used by jobs that need to inspect the
// payload, not just tally.
func ListSessionEventsByTypeSince(
	ctx context.Context,
	tx pgx.Tx,
	sessionID uuid.UUID,
	evType contracts.EventType,
	since time.Time,
) ([]SessionEventRow, error) {
	if _, ok := validEventTypes[evType]; !ok {
		return nil, fmt.Errorf("repo.session_events.list_by_type: invalid event_type %q", evType)
	}
	rows, err := tx.Query(ctx, `
		SELECT id, event_id, session_id, tenant_id, event_type, payload, occurred_at
		  FROM session_events
		 WHERE session_id = $1
		   AND event_type = $2
		   AND occurred_at >= $3
		 ORDER BY occurred_at ASC, id ASC
	`, sessionID, string(evType), since)
	if err != nil {
		return nil, fmt.Errorf("repo.session_events.list_by_type: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// scanEvents drains a pgx.Rows that selected the canonical column list.
func scanEvents(rows pgx.Rows) ([]SessionEventRow, error) {
	var out []SessionEventRow
	for rows.Next() {
		var ev SessionEventRow
		if err := rows.Scan(&ev.ID, &ev.EventID, &ev.SessionID, &ev.TenantID, &ev.EventType, &ev.Payload, &ev.OccurredAt); err != nil {
			return nil, fmt.Errorf("repo.session_events scan: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.session_events iter: %w", err)
	}
	return out, nil
}
