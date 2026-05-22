package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Stack mirrors the stacks table column-for-column. Stacks capture
// wrapped solutions — harness names, skill names, doc references, free
// notes — NEVER raw configs, env values, or MCP credentials (see
// CLAUDE.md "Locked invariants"). Classification is set by the handler
// after running the row through the trufflehog redactor; the repo
// trusts the incoming value and only validates it against the closed
// set in the CHECK constraint.
type Stack struct {
	ID             uuid.UUID `db:"id"`
	TenantID       uuid.UUID `db:"tenant_id"`
	UserID         uuid.UUID `db:"user_id"`
	Name           string    `db:"name"`
	Harnesses      []string  `db:"harnesses"`
	Skills         []string  `db:"skills"`
	Docs           []string  `db:"docs"`
	Notes          *string   `db:"notes"`
	Classification string    `db:"classification"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}

// CreateStack inserts a new stack. The caller passes an already-
// classified stack (clean | strippable | dirty) — the repo does NOT run
// trufflehog or revalidate the payload; that is the handler's job per
// issue 038. The repo validates only the closed CHECK-constraint set so
// a bad value fails fast in Go instead of as a Postgres error.
func CreateStack(ctx context.Context, tx pgx.Tx, s Stack) (Stack, error) {
	if s.TenantID == uuid.Nil {
		return Stack{}, errors.New("repo.stacks.create: tenant_id required")
	}
	if s.UserID == uuid.Nil {
		return Stack{}, errors.New("repo.stacks.create: user_id required")
	}
	if s.Name == "" {
		return Stack{}, errors.New("repo.stacks.create: name required")
	}
	if _, ok := validClassifications[s.Classification]; !ok {
		return Stack{}, fmt.Errorf("repo.stacks.create: invalid classification %q", s.Classification)
	}
	if s.Harnesses == nil {
		s.Harnesses = []string{}
	}
	if s.Skills == nil {
		s.Skills = []string{}
	}
	if s.Docs == nil {
		s.Docs = []string{}
	}

	var out Stack
	err := tx.QueryRow(ctx, `
		INSERT INTO stacks (
		  tenant_id, user_id, name, harnesses, skills, docs, notes, classification
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING
		  id, tenant_id, user_id, name, harnesses, skills, docs, notes,
		  classification, created_at, updated_at
	`,
		s.TenantID, s.UserID, s.Name, s.Harnesses, s.Skills, s.Docs, s.Notes, s.Classification,
	).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.Name, &out.Harnesses,
		&out.Skills, &out.Docs, &out.Notes, &out.Classification,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return Stack{}, fmt.Errorf("repo.stacks.create: %w", err)
	}
	return out, nil
}

// GetStack returns a stack by id. Returns pgx.ErrNoRows when the row is
// missing or hidden by RLS — callers can't distinguish, by design.
func GetStack(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Stack, error) {
	var s Stack
	err := tx.QueryRow(ctx, stackSelectAllColumns+`
		  FROM stacks
		 WHERE id = $1
	`, id).Scan(scanStackTargets(&s)...)
	if err != nil {
		return Stack{}, fmt.Errorf("repo.stacks.get: %w", err)
	}
	return s, nil
}

// ListByUser returns the caller's own stacks ordered by updated_at DESC.
// "Own" means user_id = userID; RLS narrows to tenant.
func ListByUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]Stack, error) {
	rows, err := tx.Query(ctx, stackSelectAllColumns+`
		  FROM stacks
		 WHERE user_id = $1
		 ORDER BY updated_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repo.stacks.list_by_user: %w", err)
	}
	defer rows.Close()
	return scanStacks(rows, "list_by_user")
}

// UpdateStack updates the mutable fields of a stack (name, harnesses,
// skills, docs, notes, classification) and bumps updated_at. id and
// tenant_id are immutable. user_id is also immutable — re-assigning a
// stack to another user is a delete+create, not an update.
func UpdateStack(ctx context.Context, tx pgx.Tx, s Stack) error {
	if s.ID == uuid.Nil {
		return errors.New("repo.stacks.update: id required")
	}
	if s.Name == "" {
		return errors.New("repo.stacks.update: name required")
	}
	if _, ok := validClassifications[s.Classification]; !ok {
		return fmt.Errorf("repo.stacks.update: invalid classification %q", s.Classification)
	}
	if s.Harnesses == nil {
		s.Harnesses = []string{}
	}
	if s.Skills == nil {
		s.Skills = []string{}
	}
	if s.Docs == nil {
		s.Docs = []string{}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE stacks
		   SET name = $2,
		       harnesses = $3,
		       skills = $4,
		       docs = $5,
		       notes = $6,
		       classification = $7,
		       updated_at = now()
		 WHERE id = $1
	`, s.ID, s.Name, s.Harnesses, s.Skills, s.Docs, s.Notes, s.Classification)
	if err != nil {
		return fmt.Errorf("repo.stacks.update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.stacks.update: %w", pgx.ErrNoRows)
	}
	return nil
}

// DeleteStack removes a stack by id. The stack_shares rows cascade via
// FK ON DELETE CASCADE.
func DeleteStack(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM stacks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("repo.stacks.delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repo.stacks.delete: %w", pgx.ErrNoRows)
	}
	return nil
}

// ListSharedWithUser returns the stacks that some other user has shared
// with userID. The join lives in the repo (not the handler) so RLS is
// applied to both sides — a leaked share row can't surface a stack from
// another tenant because both tables share the same `tenant_isolation`
// policy.
func ListSharedWithUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]Stack, error) {
	rows, err := tx.Query(ctx, `
		SELECT
		  s.id, s.tenant_id, s.user_id, s.name, s.harnesses, s.skills,
		  s.docs, s.notes, s.classification, s.created_at, s.updated_at
		  FROM stacks s
		  JOIN stack_shares sh ON sh.stack_id = s.id
		 WHERE sh.shared_with_user_id = $1
		 ORDER BY s.updated_at DESC, s.id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repo.stacks.list_shared_with_user: %w", err)
	}
	defer rows.Close()
	return scanStacks(rows, "list_shared_with_user")
}

// stackSelectAllColumns is the canonical column list for stacks SELECTs.
const stackSelectAllColumns = `
SELECT
  id, tenant_id, user_id, name, harnesses, skills, docs, notes,
  classification, created_at, updated_at
`

// scanStackTargets returns the slice of pointers that stackSelectAllColumns
// scans into, in field order.
func scanStackTargets(s *Stack) []any {
	return []any{
		&s.ID, &s.TenantID, &s.UserID, &s.Name, &s.Harnesses, &s.Skills,
		&s.Docs, &s.Notes, &s.Classification, &s.CreatedAt, &s.UpdatedAt,
	}
}

// scanStacks drains a pgx.Rows that selected stackSelectAllColumns.
func scanStacks(rows pgx.Rows, op string) ([]Stack, error) {
	var out []Stack
	for rows.Next() {
		var s Stack
		if err := rows.Scan(scanStackTargets(&s)...); err != nil {
			return nil, fmt.Errorf("repo.stacks.%s scan: %w", op, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo.stacks.%s iter: %w", op, err)
	}
	return out, nil
}
