package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ArchivePointer mirrors the archive_pointers table column-for-column.
//
// Unlike every other tenant-scoped table, archive_pointers carries a
// `tenant_id uuid NOT NULL` WITHOUT a foreign key to tenants(id) — see
// migrations/0001_initial.sql lines 214-219. The omission is
// intentional: the archive must survive a tenant delete so compliance
// forensics (subpoena, GDPR audit, ex-employee dispute) can still
// resolve a session UUID to its R2 object URI long after the parent
// tenant row is gone. The RLS policy on the table still enforces
// tenant isolation for live request-path reads — only the cascade is
// suppressed.
type ArchivePointer struct {
	SessionID  uuid.UUID `db:"session_id"`
	TenantID   uuid.UUID `db:"tenant_id"`
	ObjectURI  string    `db:"object_uri"`
	ArchivedAt time.Time `db:"archived_at"`
}

// InsertPointer writes a pointer row. tenant_id is read out of the
// sessions row by the archive cron (issue 047) and passed in; the repo
// does not reach back into sessions because the archive cron runs under
// WithBatch (BYPASSRLS) and the request-path RLS isolation is enforced
// at read time via the tenant_isolation policy.
//
// objectURI is opaque to the repo: the cron picks the R2 scheme
// (`r2://bucket/key`) per deploy.md "Cold archive". A blank URI is
// rejected — it would silently lose data.
func InsertPointer(ctx context.Context, tx pgx.Tx, sessionID, tenantID uuid.UUID, objectURI string) error {
	if sessionID == uuid.Nil {
		return errors.New("repo.archive_pointers.insert: session_id required")
	}
	if tenantID == uuid.Nil {
		return errors.New("repo.archive_pointers.insert: tenant_id required")
	}
	if objectURI == "" {
		return errors.New("repo.archive_pointers.insert: object_uri required")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO archive_pointers (session_id, tenant_id, object_uri)
		VALUES ($1, $2, $3)
	`, sessionID, tenantID, objectURI)
	if err != nil {
		return fmt.Errorf("repo.archive_pointers.insert: %w", err)
	}
	return nil
}

// GetForSession returns the pointer for sessionID, used by
// GET /v1/sessions/:id when the session is older than 90 days and has
// been migrated to R2. Returns pgx.ErrNoRows when missing or when RLS
// hides the row (cross-tenant access).
func GetForSession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (ArchivePointer, error) {
	var p ArchivePointer
	err := tx.QueryRow(ctx, `
		SELECT session_id, tenant_id, object_uri, archived_at
		  FROM archive_pointers
		 WHERE session_id = $1
	`, sessionID).Scan(&p.SessionID, &p.TenantID, &p.ObjectURI, &p.ArchivedAt)
	if err != nil {
		return ArchivePointer{}, fmt.Errorf("repo.archive_pointers.get: %w", err)
	}
	return p, nil
}

// ListBeforeDate returns up to `limit` pointers archived strictly
// before the cutoff, ordered by archived_at ASC (oldest first). Used by
// ops queries — e.g. "what's the oldest pointer still in Postgres?"
// or to drive the R2-object verification sweep documented in deploy.md.
func ListBeforeDate(ctx context.Context, tx pgx.Tx, before time.Time, limit int) ([]ArchivePointer, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := tx.Query(ctx, `
		SELECT session_id, tenant_id, object_uri, archived_at
		  FROM archive_pointers
		 WHERE archived_at < $1
		 ORDER BY archived_at ASC, session_id ASC
		 LIMIT $2
	`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("repo.archive_pointers.list_before_date: %w", err)
	}
	defer rows.Close()

	out := make([]ArchivePointer, 0, limit)
	for rows.Next() {
		var p ArchivePointer
		if err := rows.Scan(&p.SessionID, &p.TenantID, &p.ObjectURI, &p.ArchivedAt); err != nil {
			return nil, fmt.Errorf("repo.archive_pointers.list_before_date scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.archive_pointers.list_before_date iter: %w", err)
	}
	return out, nil
}
