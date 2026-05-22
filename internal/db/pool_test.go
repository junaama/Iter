//go:build integration

// Integration tests for the db connection layer (issue 049). These cover
// the contract of NewPool + WithTenant + WithBatch against a real
// pgvector/pg16 container.
//
// Re-uses the testcontainers harness from rls_test.go: the `setup`
// helper boots a container, applies every migration, mints the
// iter_app role, and returns superuser + iter_app *sql.DB handles. Here
// we layer a pgxpool on top of the same container so we exercise the
// real pool wiring rather than database/sql.

package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iter-dev/iter/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupWithDSN mirrors the setup() helper in rls_test.go but additionally
// returns the host-mapped DSNs for the superuser and iter_app. We can't
// reuse setup() directly because it never exposes the container's
// host port — pgxpool needs that to dial from outside the container.
//
// The duplication is acceptable: rls_test.go is the canonical
// database/sql path; this is the pgxpool path. If a third test ever
// needs the DSN it should be promoted to a shared helper file.
func setupWithDSN(t *testing.T) (super *sql.DB, superDSN, appDSN string, cleanup func()) {
	t.Helper()
	ctx := context.Background()

	migrationsDir, err := filepath.Abs("../../migrations")
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
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
		t.Fatalf("start postgres container: %v", err)
	}

	superDSN, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}

	super, err = sql.Open("pgx", superDSN)
	if err != nil {
		t.Fatalf("open superuser conn: %v", err)
	}

	migrationFiles, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil || len(migrationFiles) == 0 {
		t.Fatalf("list migrations: %v (found %d files)", err, len(migrationFiles))
	}
	for _, path := range migrationFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if _, err := super.ExecContext(ctx, stripGooseDown(string(data))); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(path), err)
		}
	}

	if _, err := super.ExecContext(ctx, fmt.Sprintf(
		"ALTER ROLE iter_app WITH LOGIN PASSWORD '%s'", appRolePassword,
	)); err != nil {
		t.Fatalf("alter iter_app password: %v", err)
	}

	appDSN = strings.Replace(superDSN, "postgres:postgres@", "iter_app:"+appRolePassword+"@", 1)

	cleanup = func() {
		_ = super.Close()
		_ = container.Terminate(ctx)
	}
	return super, superDSN, appDSN, cleanup
}

func TestWithTenant_EnforcesRLSIsolation(t *testing.T) {
	super, _, appDSN, cleanup := setupWithDSN(t)
	defer cleanup()

	ctx := context.Background()
	tenantA, tenantB := seedTwoTenants(ctx, t, super)

	appPool, err := db.NewPool(ctx, db.PoolConfig{DSN: appDSN})
	if err != nil {
		t.Fatalf("NewPool(app): %v", err)
	}
	defer appPool.Close()

	// As tenant A, count tenant A's sessions: must be ≥1.
	var countAsA int
	err = db.WithTenant(ctx, appPool, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM sessions").Scan(&countAsA)
	})
	if err != nil {
		t.Fatalf("WithTenant(A) sessions count: %v", err)
	}
	if countAsA == 0 {
		t.Fatal("expected ≥1 session visible to tenant A, got 0")
	}

	// Same query as tenant A must not see B's tenant_id anywhere.
	err = db.WithTenant(ctx, appPool, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT DISTINCT tenant_id FROM sessions")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tid string
			if err := rows.Scan(&tid); err != nil {
				return err
			}
			if tid == tenantB {
				t.Errorf("RLS leak: tenant A saw tenant B's tenant_id %s", tid)
			}
			if tid != tenantA {
				t.Errorf("RLS leak: tenant A saw foreign tenant_id %s (expected %s)", tid, tenantA)
			}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("WithTenant(A) distinct tenants: %v", err)
	}

	// And the GUC must NOT leak across transactions: a follow-up
	// WithTenant(B) on the same pool must see only B.
	err = db.WithTenant(ctx, appPool, tenantB, func(ctx context.Context, tx pgx.Tx) error {
		var seen string
		if err := tx.QueryRow(ctx, "SELECT current_setting('app.current_tenant', true)").Scan(&seen); err != nil {
			return err
		}
		if seen != tenantB {
			t.Errorf("SET LOCAL did not flip GUC: got %q want %q", seen, tenantB)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTenant(B): %v", err)
	}
}

func TestWithTenant_RollsBackOnError(t *testing.T) {
	super, _, appDSN, cleanup := setupWithDSN(t)
	defer cleanup()

	ctx := context.Background()
	tenantA, _ := seedTwoTenants(ctx, t, super)

	appPool, err := db.NewPool(ctx, db.PoolConfig{DSN: appDSN})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer appPool.Close()

	// Snapshot the row count, do an INSERT, then return an error so
	// WithTenant must roll back. The row count must be unchanged.
	preCount := countSessions(ctx, t, super, tenantA)

	sentinel := errString("user error — must roll back")
	err = db.WithTenant(ctx, appPool, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		var userID string
		if err := tx.QueryRow(ctx, "SELECT user_id FROM tenant_users WHERE tenant_id = $1 LIMIT 1", tenantA).Scan(&userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO sessions (tenant_id, user_id, harness, model, redacted_prompt, classification, started_at)
			 VALUES ($1, $2, 'claude_code', 'm', 'p', 'clean', now())`,
			tenantA, userID,
		); err != nil {
			return err
		}
		return sentinel
	})
	if err == nil {
		t.Fatal("WithTenant returned nil error; expected sentinel")
	}
	if !strings.Contains(err.Error(), "must roll back") {
		t.Fatalf("WithTenant returned unexpected error: %v", err)
	}

	postCount := countSessions(ctx, t, super, tenantA)
	if postCount != preCount {
		t.Errorf("rollback failed: sessions count went from %d to %d", preCount, postCount)
	}
}

func TestWithTenant_RejectsBadTenantID(t *testing.T) {
	// No container needed — input validation runs before acquire.
	// Pass a non-nil pool placeholder via a tiny pgxpool created against
	// an unreachable DSN; NewPool would fail, so we construct a config
	// only and call into WithTenant with nil, expecting the validation
	// path to short-circuit before touching the pool.
	err := db.WithTenant(context.Background(), &pgxpool.Pool{}, "not-a-uuid", func(context.Context, pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for non-UUID tenant_id")
	}
	if !strings.Contains(err.Error(), "invalid tenant_id") {
		t.Errorf("expected 'invalid tenant_id' error, got: %v", err)
	}
}

func TestWithBatch_NilPool(t *testing.T) {
	err := db.WithBatch(context.Background(), nil, func(context.Context, pgx.Tx) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "batch pool is nil") {
		t.Errorf("expected 'batch pool is nil' error, got: %v", err)
	}
}

// errString is a string-based error used as a rollback sentinel.
type errString string

func (e errString) Error() string { return string(e) }

func countSessions(ctx context.Context, t *testing.T, super *sql.DB, tenantID string) int {
	t.Helper()
	return countWhereTenant(ctx, t, super, "sessions", tenantID)
}
