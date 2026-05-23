//go:build integration

// Package db integration tests: tenant isolation (RLS) and cascade deletes.
//
// Per ARCHITECTURE.md §3 "Tenant isolation" and §7 "post-ingestion-leak"
// failure mode, every tenant-scoped table MUST refuse cross-tenant reads
// when the app role queries it with SET LOCAL app.current_tenant set, and
// every session_id / tenant_id deletion MUST cascade to its dependents.
//
// This file is gated by the `integration` build tag so it does not run in
// the default `go test ./...` pass — it needs Docker and is too slow for
// the inner unit-test loop. Run with `make test-rls` or
// `go test -tags=integration ./internal/db/...`.

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

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// tenantScopedTables enumerates every table that holds tenant-owned data
// and is protected by an RLS tenant_isolation policy. Adding a new
// tenant-scoped table to migrations/ without also adding it here is a
// build-break: the test loops over this list, and any table with a
// tenant_id column that isn't enumerated here will fail the
// completeness check below.
//
// Two special cases:
//   - audit_log: tenant_id is nullable with ON DELETE SET NULL, so the
//     tenant-cascade test skips it (rows survive tenant deletion with
//     nulled tenant_id; that is the intended audit-trail behavior).
//   - archive_pointers: tenant_id has no FK to tenants(id), so deleting
//     a tenant does NOT cascade. The archive cron is responsible for
//     cleanup; tested separately.
var tenantScopedTables = []string{
	"sessions",
	"session_events",
	"session_embeddings",
	"session_scores",
	"outcomes",
	"suggestions",
	"stacks",
	"stack_shares",
	"archive_pointers",
	"audit_log",
	"account_exports",
	"account_deletions",
}

// tablesThatCascadeOnTenantDelete is the subset of tenantScopedTables
// whose rows are removed when a tenant row is deleted (FK ON DELETE
// CASCADE on tenant_id). audit_log uses ON DELETE SET NULL and
// archive_pointers has no FK to tenants(id).
var tablesThatCascadeOnTenantDelete = []string{
	"sessions",
	"session_events",
	"session_embeddings",
	"session_scores",
	"outcomes",
	"suggestions",
	"stacks",
	"stack_shares",
	"account_exports",
	"account_deletions",
	"tenant_users", // global membership table, also cascades
}

// tablesThatCascadeOnSessionDelete is the set of tables that lose a row
// when its parent session is deleted.
var tablesThatCascadeOnSessionDelete = []string{
	"session_events",
	"session_embeddings",
	"session_scores",
	"outcomes",
}

const appRolePassword = "test_iter_app_pw_not_secret_only_test"

// setup boots a Postgres container, applies every migration in the
// migrations/ directory, and creates the iter_app role with a known
// password. Returns two database handles: one as the superuser (for
// setup + assertions) and one as iter_app (for RLS testing).
func setup(t *testing.T) (super, app *sql.DB, cleanup func()) {
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

	superURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	super, err = sql.Open("pgx", superURL)
	if err != nil {
		t.Fatalf("open superuser conn: %v", err)
	}

	// Apply migrations in lexical order. goose's pragmas (-- +goose Up,
	// StatementBegin/End) are SQL comments — psql / pgx run them
	// transparently, but the file also contains a `-- +goose Down`
	// section we must strip before executing, otherwise our tables
	// vanish immediately after creation.
	migrationFiles, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil || len(migrationFiles) == 0 {
		t.Fatalf("list migrations: %v (found %d files)", err, len(migrationFiles))
	}
	for _, path := range migrationFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		upSQL := stripGooseDown(string(data))
		if _, err := super.ExecContext(ctx, upSQL); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(path), err)
		}
	}

	// Mint a password for iter_app so we can open a connection as it.
	if _, err := super.ExecContext(ctx, fmt.Sprintf(
		"ALTER ROLE iter_app WITH LOGIN PASSWORD '%s'", appRolePassword,
	)); err != nil {
		t.Fatalf("alter iter_app password: %v", err)
	}

	appURL := strings.Replace(superURL, "postgres:postgres@", "iter_app:"+appRolePassword+"@", 1)
	app, err = sql.Open("pgx", appURL)
	if err != nil {
		t.Fatalf("open iter_app conn: %v", err)
	}

	cleanup = func() {
		_ = super.Close()
		_ = app.Close()
		_ = container.Terminate(ctx)
	}
	return super, app, cleanup
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

func TestSchemaCompleteness_AllTenantScopedTablesEnumerated(t *testing.T) {
	super, _, cleanup := setup(t)
	defer cleanup()

	// Pull every table in `public` that has a tenant_id column. Anything
	// not in tenantScopedTables is an oversight: a new tenant-scoped
	// table was added to migrations/ but not to this test.
	rows, err := super.Query(`
		SELECT c.table_name
		  FROM information_schema.columns c
		 WHERE c.table_schema = 'public'
		   AND c.column_name  = 'tenant_id'
		 ORDER BY c.table_name
	`)
	if err != nil {
		t.Fatalf("query schema: %v", err)
	}
	defer rows.Close()

	var found []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found = append(found, name)
	}

	// tenant_users has tenant_id but no RLS (it IS the membership
	// table — access mediation happens via join, not RLS). Excluded
	// from tenantScopedTables but visible here; we tolerate it.
	expected := map[string]struct{}{
		"tenant_users": {},
	}
	for _, name := range tenantScopedTables {
		expected[name] = struct{}{}
	}
	for _, name := range found {
		if _, ok := expected[name]; !ok {
			t.Errorf("table %q has tenant_id but is not enumerated in tenantScopedTables — add it to internal/db/rls_test.go (and add an RLS policy in migrations/ if it does not have one)", name)
		}
	}
}

func TestRLS_CrossTenantIsolation(t *testing.T) {
	super, app, cleanup := setup(t)
	defer cleanup()

	ctx := context.Background()
	tenantA, tenantB := seedTwoTenants(ctx, t, super)

	for _, table := range tenantScopedTables {
		table := table
		t.Run(table+"/tenant_A_sees_only_A", func(t *testing.T) {
			n := countAsApp(ctx, t, app, table, tenantA)
			if n == 0 {
				t.Fatalf("table %s: expected ≥1 row visible to tenant A, got 0", table)
			}
			// Cross-check: the same query as iter_app for tenant B
			// must see zero of tenant A's rows. We can't directly
			// filter by tenant_id in the query (RLS hides them); we
			// just count and assert visibility flips with the GUC.
			nB := countAsApp(ctx, t, app, table, tenantB)
			if nB == 0 {
				t.Fatalf("table %s: expected ≥1 row visible to tenant B, got 0", table)
			}
			// Verify the actual rows are disjoint by inspecting
			// tenant_ids from the iter_app's POV.
			seenA := tenantIDsVisibleAs(ctx, t, app, table, tenantA)
			seenB := tenantIDsVisibleAs(ctx, t, app, table, tenantB)
			for tid := range seenA {
				if _, leak := seenB[tid]; leak {
					t.Fatalf("table %s: tenant_id %s visible under BOTH GUCs — RLS policy is broken", table, tid)
				}
				if tid != tenantA {
					t.Fatalf("table %s: as tenant A, saw foreign tenant_id %s", table, tid)
				}
			}
			for tid := range seenB {
				if tid != tenantB {
					t.Fatalf("table %s: as tenant B, saw foreign tenant_id %s", table, tid)
				}
			}
		})
	}
}

func TestRLS_NoCurrentTenant_ReturnsZeroOrError(t *testing.T) {
	super, app, cleanup := setup(t)
	defer cleanup()

	ctx := context.Background()
	_, _ = seedTwoTenants(ctx, t, super)

	for _, table := range tenantScopedTables {
		table := table
		t.Run(table, func(t *testing.T) {
			// Without SET LOCAL app.current_tenant, current_setting
			// raises and the policy hides everything. Either zero
			// rows or an error is acceptable.
			tx, err := app.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			var n int
			err = tx.QueryRowContext(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n)
			if err != nil {
				// e.g. "unrecognized configuration parameter" — fine
				return
			}
			if n != 0 {
				t.Fatalf("table %s: expected 0 rows with no current_tenant set, got %d", table, n)
			}
		})
	}
}

func TestCascade_DeleteSession(t *testing.T) {
	super, _, cleanup := setup(t)
	defer cleanup()

	ctx := context.Background()
	tenantA, _ := seedTwoTenants(ctx, t, super)

	// Pick tenant A's session_id and snapshot the pre-delete counts.
	var sessionID string
	if err := super.QueryRowContext(ctx,
		"SELECT id FROM sessions WHERE tenant_id = $1 LIMIT 1", tenantA,
	).Scan(&sessionID); err != nil {
		t.Fatalf("select session: %v", err)
	}

	preCounts := map[string]int{}
	for _, table := range tablesThatCascadeOnSessionDelete {
		preCounts[table] = countWhereSession(ctx, t, super, table, sessionID)
		if preCounts[table] == 0 {
			t.Fatalf("seed: table %s has 0 rows for session %s; seedTwoTenants needs to insert at least one row per table per session", table, sessionID)
		}
	}

	if _, err := super.ExecContext(ctx, "DELETE FROM sessions WHERE id = $1", sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	for _, table := range tablesThatCascadeOnSessionDelete {
		got := countWhereSession(ctx, t, super, table, sessionID)
		if got != 0 {
			t.Errorf("cascade broken: table %s still has %d rows for deleted session %s (pre=%d)", table, got, sessionID, preCounts[table])
		}
	}
}

func TestCascade_DeleteTenant(t *testing.T) {
	super, _, cleanup := setup(t)
	defer cleanup()

	ctx := context.Background()
	tenantA, tenantB := seedTwoTenants(ctx, t, super)

	for _, table := range tablesThatCascadeOnTenantDelete {
		if countWhereTenant(ctx, t, super, table, tenantA) == 0 {
			t.Fatalf("seed: table %s has 0 rows for tenant A; the test cannot verify cascade if there is nothing to cascade", table)
		}
	}

	if _, err := super.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantA); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}

	for _, table := range tablesThatCascadeOnTenantDelete {
		t.Run(table, func(t *testing.T) {
			got := countWhereTenant(ctx, t, super, table, tenantA)
			if got != 0 {
				t.Errorf("cascade broken: table %s still has %d rows for deleted tenant %s", table, got, tenantA)
			}
			// Tenant B's rows must survive.
			if countWhereTenant(ctx, t, super, table, tenantB) == 0 {
				t.Errorf("collateral damage: deleting tenant A removed tenant B's rows from %s", table)
			}
		})
	}

	// audit_log: tenant_id is nullable + ON DELETE SET NULL. Rows
	// survive but tenant_id is NULL.
	var auditCount int
	if err := super.QueryRowContext(ctx,
		"SELECT count(*) FROM audit_log WHERE tenant_id IS NULL",
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	if auditCount == 0 {
		t.Errorf("audit_log: expected ≥1 row with NULL tenant_id after tenant cascade, got 0")
	}
}

// ----- helpers -----

// seedTwoTenants inserts two tenants and at least one row per
// tenant-scoped table for each tenant. Returns the two tenant UUIDs.
func seedTwoTenants(ctx context.Context, t *testing.T, super *sql.DB) (string, string) {
	t.Helper()

	var tenantA, tenantB string
	if err := super.QueryRowContext(ctx,
		"INSERT INTO tenants (name) VALUES ('tenant A') RETURNING id",
	).Scan(&tenantA); err != nil {
		t.Fatalf("insert tenant A: %v", err)
	}
	if err := super.QueryRowContext(ctx,
		"INSERT INTO tenants (name) VALUES ('tenant B') RETURNING id",
	).Scan(&tenantB); err != nil {
		t.Fatalf("insert tenant B: %v", err)
	}

	for _, tid := range []string{tenantA, tenantB} {
		var userID string
		if err := super.QueryRowContext(ctx,
			"INSERT INTO users (email, display_name) VALUES ($1, $2) RETURNING id",
			fmt.Sprintf("u-%s@example.com", tid[:8]), "User "+tid[:8],
		).Scan(&userID); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		if _, err := super.ExecContext(ctx,
			"INSERT INTO tenant_users (tenant_id, user_id, role) VALUES ($1, $2, 'member')",
			tid, userID,
		); err != nil {
			t.Fatalf("insert tenant_users: %v", err)
		}

		var sessionID string
		if err := super.QueryRowContext(ctx,
			`INSERT INTO sessions (tenant_id, user_id, harness, model, redacted_prompt, classification, started_at)
			   VALUES ($1, $2, 'claude_code', 'claude-sonnet-4', 'test prompt', 'clean', now())
			   RETURNING id`,
			tid, userID,
		).Scan(&sessionID); err != nil {
			t.Fatalf("insert session: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO session_events (tenant_id, session_id, event_type, occurred_at, payload)
			   VALUES ($1, $2, 'turn_completed', now(), '{}')`,
			tid, sessionID,
		); err != nil {
			t.Fatalf("insert session_events: %v", err)
		}

		// session_embeddings: PRIMARY KEY (session_id), so one row.
		// vector(1536) — we need exactly 1536 floats. Use zero vec.
		var zerosVec strings.Builder
		zerosVec.WriteString("[")
		for i := 0; i < 1536; i++ {
			if i > 0 {
				zerosVec.WriteString(",")
			}
			zerosVec.WriteString("0")
		}
		zerosVec.WriteString("]")
		if _, err := super.ExecContext(ctx,
			`INSERT INTO session_embeddings (session_id, tenant_id, embedding, embedding_model)
			   VALUES ($1, $2, $3::vector, 'voyage-code-3')`,
			sessionID, tid, zerosVec.String(),
		); err != nil {
			t.Fatalf("insert session_embeddings: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO session_scores (tenant_id, session_id, scorer_version, composite_score, signals, scored_at)
			   VALUES ($1, $2, 'v0-test', 0.5, '{}', now())`,
			tid, sessionID,
		); err != nil {
			t.Fatalf("insert session_scores: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO outcomes (tenant_id, session_id, outcome_type, observed_at, details)
			   VALUES ($1, $2, 'commit_landed', now(), '{}')`,
			tid, sessionID,
		); err != nil {
			t.Fatalf("insert outcomes: %v", err)
		}

		// suggestion_hash mirrors repo.SuggestionHash:
		// sha256(tenant_id_binary || source_prompt) — computed in SQL via
		// pgcrypto's digest() so this seed doesn't depend on the repo
		// package. Migration 0003 made suggestion_hash NOT NULL UNIQUE.
		// We pass tenant_id twice (once as uuid, once as text) because
		// pgx unifies the type of any single placeholder across the
		// statement; using two slots keeps both casts unambiguous.
		if _, err := super.ExecContext(ctx,
			`INSERT INTO suggestions (tenant_id, suggestion_hash, source_prompt, source_embedding, refined_prompt, evidence_session_ids, created_at)
			   VALUES ($1, digest(decode(replace($2,'-',''),'hex') || $3::bytea, 'sha256'), $3, $4::vector, $5, ARRAY[$6::uuid], now())`,
			tid, tid, "source prompt for "+tid[:8], zerosVec.String(),
			"refined prompt for "+tid[:8], sessionID,
		); err != nil {
			t.Fatalf("insert suggestions: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO stacks (tenant_id, user_id, name, harnesses, skills, docs, notes, classification, updated_at)
			   VALUES ($1, $2, $3, ARRAY['claude_code'], '{}', '{}', 'notes for '||$3, 'clean', now())`,
			tid, userID, "stack-"+tid[:8],
		); err != nil {
			t.Fatalf("insert stacks: %v", err)
		}

		// stack_shares needs two users in the same tenant to share to.
		// We have one user per tenant from above; insert a second.
		var otherUserID string
		if err := super.QueryRowContext(ctx,
			"INSERT INTO users (email, display_name) VALUES ($1, $2) RETURNING id",
			fmt.Sprintf("u2-%s@example.com", tid[:8]), "Other "+tid[:8],
		).Scan(&otherUserID); err != nil {
			t.Fatalf("insert second user: %v", err)
		}
		if _, err := super.ExecContext(ctx,
			"INSERT INTO tenant_users (tenant_id, user_id, role) VALUES ($1, $2, 'member')",
			tid, otherUserID,
		); err != nil {
			t.Fatalf("insert second tenant_users: %v", err)
		}
		// Get the stack_id we just created.
		var stackID string
		if err := super.QueryRowContext(ctx,
			"SELECT id FROM stacks WHERE tenant_id = $1 ORDER BY updated_at DESC LIMIT 1", tid,
		).Scan(&stackID); err != nil {
			t.Fatalf("select stack: %v", err)
		}
		if _, err := super.ExecContext(ctx,
			`INSERT INTO stack_shares (tenant_id, stack_id, shared_with_user_id, shared_at)
			   VALUES ($1, $2, $3, now())`,
			tid, stackID, otherUserID,
		); err != nil {
			t.Fatalf("insert stack_shares: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO archive_pointers (session_id, tenant_id, object_uri)
			   VALUES (gen_random_uuid(), $1::uuid, 'r2://test/' || $1::text)`,
			tid,
		); err != nil {
			t.Fatalf("insert archive_pointers: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO audit_log (tenant_id, actor_user_id, actor_kind, event_type, details, occurred_at)
			   VALUES ($1, $2, 'user', 'tenant_created', '{}', now())`,
			tid, userID,
		); err != nil {
			t.Fatalf("insert audit_log: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO account_exports (tenant_id, user_id, status, archive_pointer, requested_at, ready_at)
			   VALUES ($1, $2, 'ready', 'iter://account_exports/' || gen_random_uuid()::text, now(), now())`,
			tid, userID,
		); err != nil {
			t.Fatalf("insert account_exports: %v", err)
		}

		if _, err := super.ExecContext(ctx,
			`INSERT INTO account_deletions (tenant_id, user_id, requested_at, scheduled_for)
			   VALUES ($1, $2, now(), now() + interval '7 days')`,
			tid, userID,
		); err != nil {
			t.Fatalf("insert account_deletions: %v", err)
		}
	}

	return tenantA, tenantB
}

func countAsApp(ctx context.Context, t *testing.T, app *sql.DB, table, tenantID string) int {
	t.Helper()
	tx, err := app.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL app.current_tenant = '%s'", tenantID)); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func tenantIDsVisibleAs(ctx context.Context, t *testing.T, app *sql.DB, table, tenantID string) map[string]struct{} {
	t.Helper()
	tx, err := app.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL app.current_tenant = '%s'", tenantID)); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT DISTINCT tenant_id FROM %s WHERE tenant_id IS NOT NULL", table))
	if err != nil {
		t.Fatalf("query %s tenant_ids: %v", table, err)
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			t.Fatalf("scan tenant_id: %v", err)
		}
		seen[tid] = struct{}{}
	}
	return seen
}

func countWhereSession(ctx context.Context, t *testing.T, super *sql.DB, table, sessionID string) int {
	t.Helper()
	var n int
	if err := super.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE session_id = $1", table), sessionID,
	).Scan(&n); err != nil {
		t.Fatalf("count %s by session: %v", table, err)
	}
	return n
}

func countWhereTenant(ctx context.Context, t *testing.T, super *sql.DB, table, tenantID string) int {
	t.Helper()
	var n int
	if err := super.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE tenant_id = $1", table), tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count %s by tenant: %v", table, err)
	}
	return n
}
