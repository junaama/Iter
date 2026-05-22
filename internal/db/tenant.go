// Tenant and batch transaction helpers — the only sanctioned entrypoints
// for tenant-scoped (RLS) and BYPASSRLS database work.
//
// RLS contract (CLAUDE.md "Locked invariants" + DECISIONS.md Phase 3):
// every tenant-scoped query MUST run inside a transaction that has set
// `app.current_tenant` via `SET LOCAL`. The migrations install RLS
// policies that read this GUC and hide every row whose tenant_id does
// not match. WithTenant is the single function that opens such a tx and
// guarantees the GUC is set before fn runs.
//
// Calling pool.Query / pool.Exec / pool.Begin directly from the request
// path bypasses RLS and is a security bug. Code review rejects it.
//
// WithBatch is the BYPASSRLS counterpart for jobs that genuinely span
// tenants — the Modal nightly scorer (issue 046) and the archive cron
// (issue 047). It MUST connect via $DATABASE_URL_BATCH (the iter_batch
// role) so the SQL grants enforce the boundary even if the wrong helper
// is called by accident.

package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantFn is the body of a tenant-scoped transaction. The pgx.Tx is
// already bound to app.current_tenant; queries against it see only the
// caller's rows.
type TenantFn func(ctx context.Context, tx pgx.Tx) error

// BatchFn is the body of a BYPASSRLS transaction. The pgx.Tx is bound to
// the iter_batch role and sees every tenant's rows. Use sparingly.
type BatchFn func(ctx context.Context, tx pgx.Tx) error

// WithTenant opens a transaction on pool, sets app.current_tenant to the
// supplied UUID via SET LOCAL (so it auto-clears on commit/rollback),
// invokes fn with the bound tx, then commits on a nil error or rolls
// back on a non-nil error or panic. The returned error is the first of:
// begin failure, SET LOCAL failure, fn's error, commit failure.
//
// Why parse the UUID instead of accepting a string: we never want to
// interpolate untrusted bytes into a `SET LOCAL ... = '...'` statement.
// Round-tripping through uuid.Parse + uuid.String guarantees the value
// matches xxxxxxxx-xxxx-... and contains no quoting hazards. SET LOCAL
// does not bind parameters, so this validation IS the safety boundary.
func WithTenant(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID string,
	fn TenantFn,
) (retErr error) {
	if pool == nil {
		return errors.New("db.WithTenant: pool is nil")
	}
	if fn == nil {
		return errors.New("db.WithTenant: fn is nil")
	}

	parsed, err := uuid.Parse(tenantID)
	if err != nil {
		return fmt.Errorf("db.WithTenant: invalid tenant_id %q: %w", tenantID, err)
	}

	conn, err := acquire(ctx, pool, slog.Default(), defaultSlowAcquireThreshold, "WithTenant")
	if err != nil {
		return fmt.Errorf("db.WithTenant: acquire: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db.WithTenant: begin: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if retErr != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				retErr = errors.Join(retErr, fmt.Errorf("rollback: %w", rbErr))
			}
		}
	}()

	// SET LOCAL is scoped to the current transaction; on COMMIT or
	// ROLLBACK the GUC reverts. This means the connection is safe to
	// return to the pool with no carry-over tenant identity.
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL app.current_tenant = '%s'", parsed.String())); err != nil {
		return fmt.Errorf("db.WithTenant: set tenant: %w", err)
	}

	if err := fn(withTx(ctx, tx), tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db.WithTenant: commit: %w", err)
	}
	return nil
}

// WithBatch is the BYPASSRLS sibling of WithTenant. It opens a tx on the
// supplied batch pool (built from $DATABASE_URL_BATCH; iter_batch role)
// and invokes fn. It deliberately does NOT set app.current_tenant: the
// whole point of the batch path is cross-tenant work, and setting the
// GUC would silently filter results for a role that already bypasses
// RLS.
//
// At v1, only the Modal scorer (issue 046) and archive cron (issue 047)
// use this. cmd/server normally passes nil for the batch pool; calling
// WithBatch with a nil pool is an error so accidental request-path use
// fails loudly.
func WithBatch(
	ctx context.Context,
	pool *pgxpool.Pool,
	fn BatchFn,
) (retErr error) {
	if pool == nil {
		return errors.New("db.WithBatch: batch pool is nil — this binary was not configured with DATABASE_URL_BATCH")
	}
	if fn == nil {
		return errors.New("db.WithBatch: fn is nil")
	}

	conn, err := acquire(ctx, pool, slog.Default(), defaultSlowAcquireThreshold, "WithBatch")
	if err != nil {
		return fmt.Errorf("db.WithBatch: acquire: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db.WithBatch: begin: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if retErr != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				retErr = errors.Join(retErr, fmt.Errorf("rollback: %w", rbErr))
			}
		}
	}()

	if err := fn(withTx(ctx, tx), tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db.WithBatch: commit: %w", err)
	}
	return nil
}
