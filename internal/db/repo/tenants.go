package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Tenant is the storage shape for the tenants table. Mirrors
// migrations/0001_initial.sql column-for-column.
type Tenant struct {
	ID        uuid.UUID  `db:"id"`
	Name      string     `db:"name"`
	Plan      string     `db:"plan"`
	CreatedAt time.Time  `db:"created_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

// Valid plan values mirror the CHECK constraint in the migration. Kept
// as a Go-side constant so callers can reach for the typed value rather
// than re-typing the literal at every call site.
const (
	PlanFree       = "free"
	PlanTeam       = "team"
	PlanEnterprise = "enterprise"
)

// InsertTenant inserts a new tenant row and returns the persisted Tenant
// (with server-assigned id, created_at). Plan defaults to PlanFree when
// empty so admin scripts can keep the call site minimal.
func InsertTenant(ctx context.Context, tx pgx.Tx, name, plan string) (Tenant, error) {
	if name == "" {
		return Tenant{}, errors.New("repo.tenants.insert: name required")
	}
	if plan == "" {
		plan = PlanFree
	}
	var t Tenant
	err := tx.QueryRow(ctx, `
		INSERT INTO tenants (name, plan)
		VALUES ($1, $2)
		RETURNING id, name, plan, created_at, deleted_at
	`, name, plan).Scan(&t.ID, &t.Name, &t.Plan, &t.CreatedAt, &t.DeletedAt)
	if err != nil {
		return Tenant{}, fmt.Errorf("repo.tenants.insert: %w", err)
	}
	return t, nil
}

// GetTenant returns the tenant row by id. Returns pgx.ErrNoRows wrapped
// in the standard repo error format when the row is missing or
// soft-deleted callers want filtered out — note this does NOT filter
// soft-deleted rows; that decision is left to the caller (admin tools
// often need to see deleted tenants).
func GetTenant(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Tenant, error) {
	var t Tenant
	err := tx.QueryRow(ctx, `
		SELECT id, name, plan, created_at, deleted_at
		  FROM tenants
		 WHERE id = $1
	`, id).Scan(&t.ID, &t.Name, &t.Plan, &t.CreatedAt, &t.DeletedAt)
	if err != nil {
		return Tenant{}, fmt.Errorf("repo.tenants.get: %w", err)
	}
	return t, nil
}

// SoftDeleteTenant stamps deleted_at = now() without releasing the row.
// The cascade-on-hard-delete chain (sessions, scores, etc.) is preserved
// for the eventual sweeper; the soft-delete just hides the tenant from
// the request path. Returns pgx.ErrNoRows if id doesn't exist.
func SoftDeleteTenant(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE tenants SET deleted_at = now()
		 WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("repo.tenants.soft_delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.tenants.soft_delete: %w", pgx.ErrNoRows)
	}
	return nil
}

// ListTenants returns up to limit tenants ordered by created_at DESC,
// id DESC for tie-break. Pass the zero uuid as cursor to fetch the
// first page; pass the last seen id to fetch the next.
//
// The cursor is keyset-based (created_at, id) so it's stable across
// inserts: a tenant inserted while paginating cannot shift earlier
// rows into the next page, because the cursor's anchor is the
// already-fetched (created_at, id) tuple.
func ListTenants(ctx context.Context, tx pgx.Tx, limit int, cursorCreatedAt time.Time, cursorID uuid.UUID) ([]Tenant, error) {
	if limit <= 0 {
		limit = 50
	}
	var (
		rows pgx.Rows
		err  error
	)
	if cursorID == uuid.Nil {
		rows, err = tx.Query(ctx, `
			SELECT id, name, plan, created_at, deleted_at
			  FROM tenants
			 ORDER BY created_at DESC, id DESC
			 LIMIT $1
		`, limit)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT id, name, plan, created_at, deleted_at
			  FROM tenants
			 WHERE (created_at, id) < ($1, $2)
			 ORDER BY created_at DESC, id DESC
			 LIMIT $3
		`, cursorCreatedAt, cursorID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("repo.tenants.list: %w", err)
	}
	defer rows.Close()

	out := make([]Tenant, 0, limit)
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Plan, &t.CreatedAt, &t.DeletedAt); err != nil {
			return nil, fmt.Errorf("repo.tenants.list scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.tenants.list iter: %w", err)
	}
	return out, nil
}
