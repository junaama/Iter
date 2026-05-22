// Context plumbing for the active query handle.
//
// Repository functions in issues 051–053 should not care whether they
// are inside a WithTenant tx, a WithBatch tx, or — for the rare
// non-transactional read — a raw pool. They just want to call
// `db.Querier(ctx).Query(...)`.  This file is the single source of
// truth for that lookup.
//
// Convention:
//   - WithTenant / WithBatch stash the pgx.Tx in ctx before invoking fn.
//   - Repositories pull it out via Querier(ctx).
//   - If nothing is stashed, Querier panics — the caller forgot to wrap
//     their work in WithTenant/WithBatch, and falling back to a raw pool
//     would silently bypass RLS. Panicking surfaces the bug in tests
//     rather than shipping a tenant leak to production.

package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// QuerierIface is the minimal pgx interface every repository function
// should depend on. Both *pgxpool.Conn, *pgxpool.Pool, and pgx.Tx
// implement it, so the same repository function works against any
// handle without generics or wrappers.
type QuerierIface interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// ctxKey is unexported so external packages cannot stash arbitrary
// values under the same key and accidentally satisfy Querier(ctx).
type ctxKey struct{}

var txKey = ctxKey{}

// ErrNoTx means no pgx.Tx has been bound to the context by WithTenant or
// WithBatch. Request handlers should map it as an internal wiring failure.
var ErrNoTx = errors.New("db: no transaction in context")

// withTx returns a child context carrying tx. Internal to this package;
// WithTenant and WithBatch call it before invoking the user's fn.
func withTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey, tx)
}

// FromContext returns the pgx.Tx stashed by WithTenant / WithBatch, or
// nil if no tx is bound. Repository code should prefer Querier(ctx);
// FromContext is exposed for the rare case where code needs to call a
// tx-only method (e.g. tx.LargeObjects()).
func FromContext(ctx context.Context) pgx.Tx {
	if tx, ok := ctx.Value(txKey).(pgx.Tx); ok {
		return tx
	}
	return nil
}

// RequireTx returns the active pgx.Tx or a typed error when the caller forgot
// to enter the request/batch transaction wrapper.
func RequireTx(ctx context.Context) (pgx.Tx, error) {
	tx := FromContext(ctx)
	if tx == nil {
		return nil, ErrNoTx
	}
	return tx, nil
}

// Querier returns the active pgx.Tx for ctx. Panics if no tx is bound —
// callers must wrap their work in WithTenant or WithBatch. The panic is
// intentional: silently falling through to a pool here would let a
// repository function bypass RLS without the caller noticing.
func Querier(ctx context.Context) QuerierIface {
	tx, err := RequireTx(ctx)
	if err != nil {
		panic("db.Querier: no tx in context — wrap call in db.WithTenant or db.WithBatch")
	}
	return tx
}
