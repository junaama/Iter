package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// User mirrors the users table. email is citext in Postgres; the Go
// shape stores it as a normal string. Callers should not assume
// case-sensitive equality.
//
// WorkOSUserID is the WorkOS-issued "user_..." identifier (e.g.
// "user_01KS983BQM2DNAH3ZN6VTXFTDH"). Nullable because rows created via
// the AuthKit cookie flow (auth/authkit.go) predate the device-code
// exchange; populated by the POST /v1/auth/session handler.
type User struct {
	ID           uuid.UUID  `db:"id"`
	Email        string     `db:"email"`
	DisplayName  string     `db:"display_name"`
	WorkOSUserID *string    `db:"workos_user_id"`
	CreatedAt    time.Time  `db:"created_at"`
	DeletedAt    *time.Time `db:"deleted_at"`
}

// InsertUser inserts a new user and returns the persisted row.
func InsertUser(ctx context.Context, tx pgx.Tx, email, displayName string) (User, error) {
	if email == "" {
		return User{}, errors.New("repo.users.insert: email required")
	}
	if displayName == "" {
		return User{}, errors.New("repo.users.insert: display_name required")
	}
	var u User
	err := tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name)
		VALUES ($1, $2)
		RETURNING id, email::text, display_name, workos_user_id, created_at, deleted_at
	`, email, displayName).Scan(&u.ID, &u.Email, &u.DisplayName, &u.WorkOSUserID, &u.CreatedAt, &u.DeletedAt)
	if err != nil {
		return User{}, fmt.Errorf("repo.users.insert: %w", err)
	}
	return u, nil
}

// GetUser returns a user by id. Wraps pgx.ErrNoRows when missing.
func GetUser(ctx context.Context, tx pgx.Tx, id uuid.UUID) (User, error) {
	var u User
	err := tx.QueryRow(ctx, `
		SELECT id, email::text, display_name, workos_user_id, created_at, deleted_at
		  FROM users
		 WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.DisplayName, &u.WorkOSUserID, &u.CreatedAt, &u.DeletedAt)
	if err != nil {
		return User{}, fmt.Errorf("repo.users.get: %w", err)
	}
	return u, nil
}

// GetUserByEmail looks up by citext email (case-insensitive). Wraps
// pgx.ErrNoRows when missing. Used by signup / WorkOS callback to
// resolve the principal.
func GetUserByEmail(ctx context.Context, tx pgx.Tx, email string) (User, error) {
	var u User
	err := tx.QueryRow(ctx, `
		SELECT id, email::text, display_name, workos_user_id, created_at, deleted_at
		  FROM users
		 WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.WorkOSUserID, &u.CreatedAt, &u.DeletedAt)
	if err != nil {
		return User{}, fmt.Errorf("repo.users.get_by_email: %w", err)
	}
	return u, nil
}

// GetUserByWorkOSID looks up by the WorkOS-issued "user_..." id. Wraps
// pgx.ErrNoRows when missing. Used by POST /v1/auth/session to resolve
// (or seed) the Iter user that a WorkOS access token maps to.
func GetUserByWorkOSID(ctx context.Context, tx pgx.Tx, workosID string) (User, error) {
	if workosID == "" {
		return User{}, errors.New("repo.users.get_by_workos_id: workos_user_id required")
	}
	var u User
	err := tx.QueryRow(ctx, `
		SELECT id, email::text, display_name, workos_user_id, created_at, deleted_at
		  FROM users
		 WHERE workos_user_id = $1
	`, workosID).Scan(&u.ID, &u.Email, &u.DisplayName, &u.WorkOSUserID, &u.CreatedAt, &u.DeletedAt)
	if err != nil {
		return User{}, fmt.Errorf("repo.users.get_by_workos_id: %w", err)
	}
	return u, nil
}

// InsertUserWithWorkOS inserts a new user row stamped with the WorkOS
// identifier. Used by POST /v1/auth/session for first-time device-code
// sign-ins. The (email, workos_user_id) pair must both be unique — the
// caller is responsible for resolving conflicts (typically: look up by
// workos_user_id first; if found, reuse; else insert).
func InsertUserWithWorkOS(ctx context.Context, tx pgx.Tx, email, displayName, workosID string) (User, error) {
	if email == "" {
		return User{}, errors.New("repo.users.insert_with_workos: email required")
	}
	if displayName == "" {
		return User{}, errors.New("repo.users.insert_with_workos: display_name required")
	}
	if workosID == "" {
		return User{}, errors.New("repo.users.insert_with_workos: workos_user_id required")
	}
	var u User
	err := tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, workos_user_id)
		VALUES ($1, $2, $3)
		RETURNING id, email::text, display_name, workos_user_id, created_at, deleted_at
	`, email, displayName, workosID).Scan(&u.ID, &u.Email, &u.DisplayName, &u.WorkOSUserID, &u.CreatedAt, &u.DeletedAt)
	if err != nil {
		return User{}, fmt.Errorf("repo.users.insert_with_workos: %w", err)
	}
	return u, nil
}

// SoftDeleteUser stamps deleted_at = now(). Like SoftDeleteTenant this
// preserves rows for the cascade sweeper while hiding the user from
// the request path.
func SoftDeleteUser(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users SET deleted_at = now()
		 WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("repo.users.soft_delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.users.soft_delete: %w", pgx.ErrNoRows)
	}
	return nil
}
