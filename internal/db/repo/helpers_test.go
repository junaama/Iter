//go:build integration

package repo_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/db"
)

// poolHandle wraps a *pgxpool.Pool with helpers shared by every test
// file in this package. Kept as a small struct so future helpers
// (Healthcheck, etc.) can hang off the same value without touching
// signatures.
type poolHandle struct {
	pool *pgxpool.Pool
}

func (h *poolHandle) Close() { h.pool.Close() }

// openSuperPool builds an iter app-style pool against the superuser DSN.
// Tenants tests don't care about RLS; this just gives us a pool that
// can serve pgx.Tx values directly. Repo functions are tx-only so we
// open a tx manually and commit at the end of each scenario.
func openSuperPool(t *testing.T, ctx context.Context, dsn string) *poolHandle {
	t.Helper()
	p, err := db.NewPool(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("openSuperPool: %v", err)
	}
	return &poolHandle{pool: p}
}

// mustTx opens a transaction on the pool, invokes fn, and commits.
// Rolls back on error. Used by tests that don't need RLS — the
// tenants and users tables are not tenant-scoped.
func mustTx(t *testing.T, ctx context.Context, h *poolHandle, fn func(pgx.Tx) error) {
	t.Helper()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("tx fn: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
