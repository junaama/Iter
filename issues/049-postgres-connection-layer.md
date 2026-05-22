---
type: AFK
depends-on:
  - 048-cmd-server-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Postgres connection layer with `pgxpool` + PgBouncer transaction mode; helper to `SET LOCAL app.current_tenant`"). RLS is the locked invariant — `CLAUDE.md` "Working with `migrations/`" and `DECISIONS.md` Phase 3 reiterate that every tenant-scoped transaction MUST `SET LOCAL app.current_tenant = '<uuid>'`.

## What to build

A `pgxpool`-based connection layer in `internal/db` that the rest of the binary uses for every Postgres call, plus a tenant helper that makes the RLS contract impossible to skip.

Specifically:

1. **Pool construction**: `internal/db.NewPool(ctx, cfg) (*pgxpool.Pool, error)` reading `DATABASE_URL` (request path) and configuring sensible defaults: `MaxConns=25`, `MinConns=2`, `MaxConnLifetime=1h`, `MaxConnIdleTime=30m`, `HealthCheckPeriod=1m`. Pool is sized for PgBouncer **transaction mode** (per `DECISIONS.md` Phase 2). Document the prepared-statement caveat (transaction-mode PgBouncer disallows server-side prepared statements; pgx must run with `statement_cache_mode=describe` or equivalent).
2. **Tenant helper**: `db.WithTenant(ctx, pool, tenantID, fn func(pgx.Tx) error) error` opens a transaction, runs `SET LOCAL app.current_tenant = $1`, invokes `fn`, commits or rolls back. The helper is the **only** sanctioned entrypoint for tenant-scoped queries; calling `pool.Query` directly bypasses RLS and must be reserved for `iter_batch` paths.
3. **Batch helper**: `db.WithBatch(ctx, batchPool, fn func(pgx.Tx) error) error` for `iter_batch` BYPASSRLS paths (scoring batch, archive cron). Reads `DATABASE_URL_BATCH` (per `deploy.md`).
4. **Health check**: extend `internal/api.HealthHandler` from 048 to actually run `SELECT 1` against the pool and report `"db": "ok" | "down"`. Status 503 when `db` is down.
5. **Testcontainers integration test**: spin a `pgvector/pgvector:pg16` container, apply migrations, exercise the tenant helper with two tenants and assert RLS isolation. Re-use whatever pattern issue 004 lands; if 004 is unmerged, write a minimal local helper and consolidate later.
6. **Pool metric hooks**: pgx `BeforeAcquire` / `AfterRelease` log slow acquires (>50ms) via `slog`; metrics integration deferred to Step 7.

## Acceptance criteria

- [ ] `internal/db.NewPool` configured for transaction-mode PgBouncer
- [ ] `internal/db.WithTenant` opens a tx, runs `SET LOCAL app.current_tenant`, commits/rollbacks correctly on success/error
- [ ] `internal/db.WithBatch` exists for BYPASSRLS paths (separate `DATABASE_URL_BATCH` env)
- [ ] `/health` returns `db: ok` / 200 when DB reachable, `db: down` / 503 otherwise
- [ ] Testcontainers test verifies: a tx run via `WithTenant(A)` cannot read tenant B's rows; same query run via `WithBatch` reads both (BYPASSRLS path)
- [ ] Slow-acquire logging via pgx pool hooks
- [ ] Prepared-statement caveat for transaction-mode PgBouncer documented in `internal/db/doc.go` or `DECISIONS.md`
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/048-cmd-server-skeleton.md`

(Soft-depends on `issues/in-progress/004-rls-cascade-delete-verification.md` — share testcontainers harness when both land. Not a hard blocker.)

## User stories addressed

Underpins every request-path query. The tenant helper is what makes the RLS invariant enforceable — without it, every handler in Step 4 risks bypassing isolation.
