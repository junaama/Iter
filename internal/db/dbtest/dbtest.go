// Package dbtest is the shared testcontainers harness for the integration
// tests in internal/db/... Issue 051 introduced it; issues 052 and 053
// will import it to avoid duplicating the container/migration boot.
//
// Build tag: only the rest of the repo's tests build this in via
// `//go:build integration` consumers. The package itself is tag-free so
// it can be referenced from any test file under the integration tag
// without redeclaring the boot logic.

package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // sql.Open("pgx", ...)
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/iter-dev/iter/internal/db"
)

// AppRolePassword is the test-only password applied to the iter_app role
// after migrations run. Mirrors the constant used by rls_test.go so
// repo tests can keep using the same handle layout.
const AppRolePassword = "test_iter_app_pw_not_secret_only_test"

// TestDB carries the live container handles for an integration test.
// Cleanup() tears everything down; tests must `defer Cleanup()`.
//
// Super is a database/sql handle bound to the postgres superuser; use
// it for setup work that needs to bypass RLS (seeding tenants, asserting
// invariants from outside the repo path).
//
// AppPool is a pgxpool bound to the iter_app role; use it as the input
// to db.WithTenant when exercising repository functions the way request
// handlers do.
//
// BatchPool is nil by default; tests that need WithBatch can call
// NewBatchPool() to mint one bound to the iter_batch role.
type TestDB struct {
	Super     *sql.DB
	SuperDSN  string
	AppPool   *pgxpool.Pool
	AppDSN    string
	cleanupFn func()
}

// Cleanup releases everything Setup acquired. Idempotent.
func (t *TestDB) Cleanup() {
	if t.cleanupFn != nil {
		t.cleanupFn()
		t.cleanupFn = nil
	}
}

// Setup boots a pgvector/pg16 container, applies every migration under
// migrationsRelative (resolved relative to the calling test file), mints
// the iter_app role with AppRolePassword, and returns a TestDB with both
// the superuser sql.DB and an iter_app pgxpool ready for db.WithTenant.
//
// migrationsRelative is typically "../../../migrations" for tests under
// internal/db/repo/. The helper accepts it as a parameter so future
// consumers can live deeper in the tree without changing the API.
func Setup(t *testing.T, migrationsRelative string) *TestDB {
	t.Helper()

	ctx := context.Background()

	migrationsDir, err := filepath.Abs(migrationsRelative)
	if err != nil {
		t.Fatalf("dbtest: resolve migrations dir: %v", err)
	}

	container, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("iter_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("dbtest: start postgres container: %v", err)
	}

	superDSN, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dbtest: connection string: %v", err)
	}

	super, err := sql.Open("pgx", superDSN)
	if err != nil {
		t.Fatalf("dbtest: open superuser conn: %v", err)
	}

	if err := applyMigrations(ctx, super, migrationsDir); err != nil {
		t.Fatalf("dbtest: apply migrations: %v", err)
	}

	if _, err := super.ExecContext(ctx, fmt.Sprintf(
		"ALTER ROLE iter_app WITH LOGIN PASSWORD '%s'", AppRolePassword,
	)); err != nil {
		t.Fatalf("dbtest: alter iter_app password: %v", err)
	}

	appDSN := strings.Replace(superDSN, "postgres:postgres@", "iter_app:"+AppRolePassword+"@", 1)

	appPool, err := db.NewPool(ctx, db.PoolConfig{DSN: appDSN})
	if err != nil {
		t.Fatalf("dbtest: open iter_app pool: %v", err)
	}

	cleanup := func() {
		appPool.Close()
		_ = super.Close()
		_ = container.Terminate(ctx)
	}

	return &TestDB{
		Super:     super,
		SuperDSN:  superDSN,
		AppPool:   appPool,
		AppDSN:    appDSN,
		cleanupFn: cleanup,
	}
}

// applyMigrations runs every *.sql under dir in lexical order against
// the superuser handle. goose's pragmas are SQL comments — pgx ignores
// them — but the file's `-- +goose Down` section must be stripped or
// the Down body executes immediately after the Up and drops everything.
func applyMigrations(ctx context.Context, super *sql.DB, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("list migrations under %s: %w (found %d)", dir, err, len(files))
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(path), err)
		}
		if _, err := super.ExecContext(ctx, stripGooseDown(string(data))); err != nil {
			return fmt.Errorf("apply %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// stripGooseDown removes the `-- +goose Down` section so the migration
// applied via raw exec doesn't also drop everything it just created.
func stripGooseDown(sql string) string {
	idx := strings.Index(sql, "-- +goose Down")
	if idx < 0 {
		return sql
	}
	return sql[:idx]
}

// SeedTenant inserts a tenant with the given name and returns its UUID.
// Runs as superuser (no RLS to satisfy). Test helper only.
func (t *TestDB) SeedTenant(ctx context.Context, tb testing.TB, name string) string {
	tb.Helper()
	var id string
	if err := t.Super.QueryRowContext(ctx,
		"INSERT INTO tenants (name) VALUES ($1) RETURNING id", name,
	).Scan(&id); err != nil {
		tb.Fatalf("dbtest: seed tenant %q: %v", name, err)
	}
	return id
}

// SeedUser inserts a user and returns its UUID. citext on email is
// case-insensitive; callers should pass already-lowercased emails when
// the test cares about exact-string round-tripping.
func (t *TestDB) SeedUser(ctx context.Context, tb testing.TB, email, displayName string) string {
	tb.Helper()
	var id string
	if err := t.Super.QueryRowContext(ctx,
		"INSERT INTO users (email, display_name) VALUES ($1, $2) RETURNING id", email, displayName,
	).Scan(&id); err != nil {
		tb.Fatalf("dbtest: seed user %q: %v", email, err)
	}
	return id
}

// SeedMembership joins user to tenant with the given role.
func (t *TestDB) SeedMembership(ctx context.Context, tb testing.TB, tenantID, userID, role string) {
	tb.Helper()
	if _, err := t.Super.ExecContext(ctx,
		"INSERT INTO tenant_users (tenant_id, user_id, role) VALUES ($1, $2, $3)",
		tenantID, userID, role,
	); err != nil {
		tb.Fatalf("dbtest: seed membership: %v", err)
	}
}
