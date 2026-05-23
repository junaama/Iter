package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iter-dev/iter/pkg/contracts"
	_ "modernc.org/sqlite"
)

const defaultCaptureWALListLimit = 100

// CaptureWALEntry is a durable local capture event awaiting cloud ack.
type CaptureWALEntry struct {
	ID    int64
	Event CaptureWALEvent
}

// CaptureWALEvent is the daemon event shape persisted before cloud publish.
type CaptureWALEvent struct {
	SessionID  uuid.UUID
	EventType  contracts.EventType
	OccurredAt time.Time
	Payload    map[string]any
}

// CaptureWAL stores captured daemon events locally until WebSocket publish ack.
type CaptureWAL struct {
	db *sql.DB
}

// OpenCaptureWAL opens or creates the local capture WAL database at path.
func OpenCaptureWAL(ctx context.Context, path string) (*CaptureWAL, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("capture WAL path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	wal := &CaptureWAL{db: db}
	if err := wal.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return wal, nil
}

func (w *CaptureWAL) init(ctx context.Context) error {
	for _, stmt := range []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`CREATE TABLE IF NOT EXISTS capture_wal_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_key TEXT NOT NULL UNIQUE,
			session_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			occurred_at TEXT NOT NULL,
			payload_json BLOB NOT NULL,
			created_at TEXT NOT NULL,
			sent_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_capture_wal_unsent_fifo
			ON capture_wal_events(sent_at, id)
			WHERE sent_at IS NULL`,
	} {
		if _, err := w.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the underlying SQLite handle.
func (w *CaptureWAL) Close() error {
	if w == nil || w.db == nil {
		return nil
	}
	return w.db.Close()
}

// Append stores event durably. Re-appending the same event is idempotent and
// returns the original row.
func (w *CaptureWAL) Append(ctx context.Context, event CaptureWALEvent) (CaptureWALEntry, error) {
	payload, key, err := captureWALEventKey(event)
	if err != nil {
		return CaptureWALEntry{}, err
	}
	occurredAt := event.OccurredAt.UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := w.db.ExecContext(ctx, `INSERT OR IGNORE INTO capture_wal_events
		(event_key, session_id, event_type, occurred_at, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key,
		event.SessionID.String(),
		string(event.EventType),
		occurredAt,
		payload,
		now,
	); err != nil {
		return CaptureWALEntry{}, err
	}
	return w.entryByKey(ctx, key)
}

// AppendBatch stores each event durably and preserves input order in the result.
func (w *CaptureWAL) AppendBatch(ctx context.Context, events []CaptureWALEvent) ([]CaptureWALEntry, error) {
	entries := make([]CaptureWALEntry, 0, len(events))
	for _, event := range events {
		entry, err := w.Append(ctx, event)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Unsent returns events that have not yet been acked, oldest first.
func (w *CaptureWAL) Unsent(ctx context.Context, limit int) ([]CaptureWALEntry, error) {
	if limit <= 0 {
		limit = defaultCaptureWALListLimit
	}
	rows, err := w.db.QueryContext(ctx, `SELECT id, session_id, event_type, occurred_at, payload_json
		FROM capture_wal_events
		WHERE sent_at IS NULL
		ORDER BY id ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCaptureWALEntries(rows)
}

// MarkSent marks WAL rows as delivered after the server acknowledges publish.
func (w *CaptureWAL) MarkSent(ctx context.Context, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	sentAt := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("invalid capture WAL id %d", id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE capture_wal_events
			SET sent_at = COALESCE(sent_at, ?)
			WHERE id = ?`, sentAt, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (w *CaptureWAL) entryByKey(ctx context.Context, key string) (CaptureWALEntry, error) {
	row := w.db.QueryRowContext(ctx, `SELECT id, session_id, event_type, occurred_at, payload_json
		FROM capture_wal_events
		WHERE event_key = ?`, key)
	return scanCaptureWALEntry(row)
}

func captureWALEventKey(event CaptureWALEvent) ([]byte, string, error) {
	if event.SessionID == uuid.Nil {
		return nil, "", errors.New("capture WAL event session id is required")
	}
	if event.EventType == "" {
		return nil, "", errors.New("capture WAL event type is required")
	}
	if event.OccurredAt.IsZero() {
		return nil, "", errors.New("capture WAL event occurred_at is required")
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		event.SessionID.String(),
		string(event.EventType),
		event.OccurredAt.UTC().Format(time.RFC3339Nano),
		string(payload),
	}, "\x00")))
	return payload, hex.EncodeToString(sum[:]), nil
}

type captureWALScanner interface {
	Scan(dest ...any) error
}

func scanCaptureWALEntry(row captureWALScanner) (CaptureWALEntry, error) {
	var (
		entry      CaptureWALEntry
		sessionID  string
		eventType  string
		occurredAt string
		payload    []byte
	)
	if err := row.Scan(&entry.ID, &sessionID, &eventType, &occurredAt, &payload); err != nil {
		return CaptureWALEntry{}, err
	}
	parsedSessionID, err := uuid.Parse(sessionID)
	if err != nil {
		return CaptureWALEntry{}, err
	}
	parsedOccurredAt, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return CaptureWALEntry{}, err
	}
	var parsedPayload map[string]any
	if err := json.Unmarshal(payload, &parsedPayload); err != nil {
		return CaptureWALEntry{}, err
	}
	entry.Event = CaptureWALEvent{
		SessionID:  parsedSessionID,
		EventType:  contracts.EventType(eventType),
		OccurredAt: parsedOccurredAt.UTC(),
		Payload:    parsedPayload,
	}
	return entry, nil
}

func scanCaptureWALEntries(rows *sql.Rows) ([]CaptureWALEntry, error) {
	var entries []CaptureWALEntry
	for rows.Next() {
		entry, err := scanCaptureWALEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
