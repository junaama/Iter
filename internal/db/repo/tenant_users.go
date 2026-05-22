package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TenantUser is one row of the membership table.
type TenantUser struct {
	TenantID uuid.UUID `db:"tenant_id"`
	UserID   uuid.UUID `db:"user_id"`
	Role     string    `db:"role"`
	JoinedAt time.Time `db:"joined_at"`
}

// Valid role values mirror the CHECK constraint in 0001_initial.sql.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// validRoles is the closed set of values the schema accepts. Checked
// client-side so a bad role surfaces as a domain error rather than as a
// raw Postgres constraint violation.
var validRoles = map[string]struct{}{
	RoleOwner:  {},
	RoleAdmin:  {},
	RoleMember: {},
}

// InsertTenantUser adds a (tenant, user, role) row. Idempotent inserts
// are not provided at this layer — the schema's composite PK rejects
// duplicates and callers must decide whether that's an error.
func InsertTenantUser(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, role string) (TenantUser, error) {
	if _, ok := validRoles[role]; !ok {
		return TenantUser{}, fmt.Errorf("repo.tenant_users.insert: invalid role %q", role)
	}
	var tu TenantUser
	err := tx.QueryRow(ctx, `
		INSERT INTO tenant_users (tenant_id, user_id, role)
		VALUES ($1, $2, $3)
		RETURNING tenant_id, user_id, role, joined_at
	`, tenantID, userID, role).Scan(&tu.TenantID, &tu.UserID, &tu.Role, &tu.JoinedAt)
	if err != nil {
		return TenantUser{}, fmt.Errorf("repo.tenant_users.insert: %w", err)
	}
	return tu, nil
}

// GetTenantUser returns the membership row for (tenant, user), or
// pgx.ErrNoRows when the user is not in the tenant.
func GetTenantUser(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID) (TenantUser, error) {
	var tu TenantUser
	err := tx.QueryRow(ctx, `
		SELECT tenant_id, user_id, role, joined_at
		  FROM tenant_users
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID).Scan(&tu.TenantID, &tu.UserID, &tu.Role, &tu.JoinedAt)
	if err != nil {
		return TenantUser{}, fmt.Errorf("repo.tenant_users.get: %w", err)
	}
	return tu, nil
}

// ListTenantUsersByTenant returns every membership row for tenantID
// ordered by joined_at ASC for stable display.
func ListTenantUsersByTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]TenantUser, error) {
	rows, err := tx.Query(ctx, `
		SELECT tenant_id, user_id, role, joined_at
		  FROM tenant_users
		 WHERE tenant_id = $1
		 ORDER BY joined_at ASC, user_id ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("repo.tenant_users.list_by_tenant: %w", err)
	}
	defer rows.Close()
	return scanTenantUsers(rows)
}

// ListTenantUsersByUser returns every membership row for userID
// ordered by joined_at ASC. Used by the CLI device-code flow to show
// "which tenants can I join."
func ListTenantUsersByUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]TenantUser, error) {
	rows, err := tx.Query(ctx, `
		SELECT tenant_id, user_id, role, joined_at
		  FROM tenant_users
		 WHERE user_id = $1
		 ORDER BY joined_at ASC, tenant_id ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repo.tenant_users.list_by_user: %w", err)
	}
	defer rows.Close()
	return scanTenantUsers(rows)
}

// UpdateTenantUserRole flips the role of an existing membership.
// Returns pgx.ErrNoRows if the membership doesn't exist.
func UpdateTenantUserRole(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, role string) error {
	if _, ok := validRoles[role]; !ok {
		return fmt.Errorf("repo.tenant_users.update_role: invalid role %q", role)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE tenant_users SET role = $3
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID, role)
	if err != nil {
		return fmt.Errorf("repo.tenant_users.update_role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.tenant_users.update_role: %w", pgx.ErrNoRows)
	}
	return nil
}

// DeleteTenantUser removes a (tenant, user) membership row. Returns
// pgx.ErrNoRows if the membership did not exist.
func DeleteTenantUser(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		DELETE FROM tenant_users
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("repo.tenant_users.delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.tenant_users.delete: %w", pgx.ErrNoRows)
	}
	return nil
}

// scanTenantUsers is the shared row-scan body for the two list
// functions. Pulled out to keep each list function's body trivial.
func scanTenantUsers(rows pgx.Rows) ([]TenantUser, error) {
	var out []TenantUser
	for rows.Next() {
		var tu TenantUser
		if err := rows.Scan(&tu.TenantID, &tu.UserID, &tu.Role, &tu.JoinedAt); err != nil {
			return nil, fmt.Errorf("repo.tenant_users scan: %w", err)
		}
		out = append(out, tu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.tenant_users iter: %w", err)
	}
	return out, nil
}
