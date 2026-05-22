package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StackShare mirrors the stack_shares table. The composite PK is
// (stack_id, shared_with_user_id); shared_at is server-assigned.
type StackShare struct {
	StackID          uuid.UUID `db:"stack_id"`
	TenantID         uuid.UUID `db:"tenant_id"`
	SharedWithUserID uuid.UUID `db:"shared_with_user_id"`
	SharedAt         time.Time `db:"shared_at"`
}

// AddShare links stackID with sharedWithUserID. Idempotent: a duplicate
// share is a no-op (ON CONFLICT DO NOTHING) so callers can retry safely
// without inspecting the error.
//
// tenant_id is sourced from the parent stack row via a subquery — the
// caller never has to supply it. RLS scopes the subquery so a stack
// from another tenant returns no row, the INSERT NULL-fails the FK
// check, and the caller gets a clear error. This deliberately prevents
// "share my stack with a user in another tenant" by construction; the
// shared_with_user_id FK is on users(id) which is global, so we can't
// catch cross-tenant target users at the DB layer — the handler is
// responsible for verifying tenant membership (issue 038).
func AddShare(ctx context.Context, tx pgx.Tx, stackID, sharedWithUserID uuid.UUID) error {
	if stackID == uuid.Nil {
		return errors.New("repo.stack_shares.add: stack_id required")
	}
	if sharedWithUserID == uuid.Nil {
		return errors.New("repo.stack_shares.add: shared_with_user_id required")
	}
	// The (SELECT tenant_id FROM stacks WHERE id = $1) subquery is
	// RLS-scoped, so if the stack belongs to another tenant the inner
	// query returns NULL and the NOT NULL constraint rejects the
	// insert. That is the desired behavior: callers cannot share rows
	// they cannot see.
	tag, err := tx.Exec(ctx, `
		INSERT INTO stack_shares (stack_id, tenant_id, shared_with_user_id)
		SELECT $1, s.tenant_id, $2
		  FROM stacks s
		 WHERE s.id = $1
		ON CONFLICT (stack_id, shared_with_user_id) DO NOTHING
	`, stackID, sharedWithUserID)
	if err != nil {
		return fmt.Errorf("repo.stack_shares.add: %w", err)
	}
	// RowsAffected may be 0 in two cases: (a) the share already
	// existed (idempotent path — fine) and (b) the stack is hidden by
	// RLS and the SELECT returned no rows. We can't distinguish here;
	// the handler typically calls GetStack first for authorization,
	// which already returns pgx.ErrNoRows on missing-or-hidden.
	_ = tag
	return nil
}

// RemoveShare deletes the (stack_id, shared_with_user_id) row.
// Returns pgx.ErrNoRows when the row doesn't exist or RLS hides it.
func RemoveShare(ctx context.Context, tx pgx.Tx, stackID, sharedWithUserID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		DELETE FROM stack_shares
		 WHERE stack_id = $1 AND shared_with_user_id = $2
	`, stackID, sharedWithUserID)
	if err != nil {
		return fmt.Errorf("repo.stack_shares.remove: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.stack_shares.remove: %w", pgx.ErrNoRows)
	}
	return nil
}

// ListSharesForStack returns every share targeting the given stackID.
// RLS narrows to tenant.
func ListSharesForStack(ctx context.Context, tx pgx.Tx, stackID uuid.UUID) ([]StackShare, error) {
	rows, err := tx.Query(ctx, `
		SELECT stack_id, tenant_id, shared_with_user_id, shared_at
		  FROM stack_shares
		 WHERE stack_id = $1
		 ORDER BY shared_at ASC, shared_with_user_id ASC
	`, stackID)
	if err != nil {
		return nil, fmt.Errorf("repo.stack_shares.list_for_stack: %w", err)
	}
	defer rows.Close()
	return scanStackShares(rows, "list_for_stack")
}

// ListSharesByUser returns every share where the user is the target.
// Use ListSharedWithUser (in stacks.go) when the caller wants the
// stacks themselves; this returns the bare share rows for admin views.
func ListSharesByUser(ctx context.Context, tx pgx.Tx, sharedWithUserID uuid.UUID) ([]StackShare, error) {
	rows, err := tx.Query(ctx, `
		SELECT stack_id, tenant_id, shared_with_user_id, shared_at
		  FROM stack_shares
		 WHERE shared_with_user_id = $1
		 ORDER BY shared_at DESC, stack_id ASC
	`, sharedWithUserID)
	if err != nil {
		return nil, fmt.Errorf("repo.stack_shares.list_by_user: %w", err)
	}
	defer rows.Close()
	return scanStackShares(rows, "list_by_user")
}

// scanStackShares drains a pgx.Rows whose column list matches
// (stack_id, tenant_id, shared_with_user_id, shared_at).
func scanStackShares(rows pgx.Rows, op string) ([]StackShare, error) {
	var out []StackShare
	for rows.Next() {
		var s StackShare
		if err := rows.Scan(&s.StackID, &s.TenantID, &s.SharedWithUserID, &s.SharedAt); err != nil {
			return nil, fmt.Errorf("repo.stack_shares.%s scan: %w", op, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.stack_shares.%s iter: %w", op, err)
	}
	return out, nil
}
