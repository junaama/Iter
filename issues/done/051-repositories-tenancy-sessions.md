---
type: AFK
depends-on:
  - 049-postgres-connection-layer
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Repository functions per table; testcontainers-backed tests, not mocks"). See §3 Tables for the schema and `migrations/0001_initial.sql` for the canonical column definitions. `testing.md` "Integration tests" enumerates the repository layer as the primary integration target.

## What to build

The first repository slice. Establishes the repo conventions (signature shape, error types, transaction handling, test harness) and covers two table groups: tenancy and sessions.

Tables in scope:

- `tenants`, `users`, `tenant_users` — tenancy
- `sessions` (including the self-referential `parent_session_id` FK), `session_events` (append-only lifecycle log)

Specifically:

1. **Repo conventions** (lock and document in `internal/db/doc.go`):
   - One file per table: `internal/db/<table>.go`.
   - Each repo function takes a `pgx.Tx` (NOT `*pgxpool.Pool` directly) — callers run inside `db.WithTenant` or `db.WithBatch`. This makes RLS bypass impossible by accident.
   - Standard signatures: `InsertX(tx, x) (id, err)`, `GetX(tx, id) (x, err)`, `ListX(tx, filter) ([]x, err)`, `UpdateX(tx, x) error`, `DeleteX(tx, id) error`. Not every table needs all five.
   - Errors wrap with `fmt.Errorf("db.<table>.<op>: %w", err)`. `pgx.ErrNoRows` is the canonical not-found.
   - Row scanning via `pgx.RowToStructByName[T]` where the struct lives in `internal/db/types` (NOT `pkg/contracts` — contracts are wire types, not DB types).
2. **Tenancy repos**:
   - `tenants.go`: Insert (admin path only — uses `WithBatch`), Get, GetBySlug.
   - `users.go`: Insert, Get, GetByEmail (citext).
   - `tenant_users.go`: Insert, ListByTenant, ListByUser, Delete, role updates.
3. **Sessions repos**:
   - `sessions.go`: Insert, Get, List (filtered by user/time range), ListSubagents (by `parent_session_id`), Delete.
   - `session_events.go`: Insert (append-only — never update or delete from request path), ListBySession (chronological), ListByTypeSince.
4. **Testcontainers test harness** (lives in `internal/db/dbtest/`):
   - `dbtest.New(t) *TestDB` — spins a pgvector pg16 container, applies migrations, returns helpers for seeding tenants/users.
   - Reused by 052 and 053.
5. **Tests**:
   - Per-repo CRUD tests against the testcontainers pg.
   - RLS spot-checks: insert from tenant A, attempt read as tenant B → empty (re-use 004's harness if available; otherwise local).
   - Subagent self-reference: insert parent, insert child with `parent_session_id`, verify `ListSubagents` returns the child.
   - `session_events` ordering: insert events out-of-order, `ListBySession` returns them in `occurred_at` order.

## Acceptance criteria

- [ ] Repo conventions documented in `internal/db/doc.go`
- [ ] `internal/db/dbtest/` helper exists; new tables can be added in subsequent issues by importing it
- [ ] Five repo files exist: `tenants.go`, `users.go`, `tenant_users.go`, `sessions.go`, `session_events.go`
- [ ] Every repo function takes `pgx.Tx` (not `*pgxpool.Pool`) and is callable inside `WithTenant` / `WithBatch`
- [ ] Each table has at least one testcontainers test exercising the full lifecycle (insert → get → list → delete where applicable)
- [ ] RLS isolation verified at the repo level (tenant A insert, tenant B read returns zero rows)
- [ ] Subagent self-reference covered: `ListSubagents` returns children for a given parent
- [ ] `session_events` ordering verified via out-of-order insert
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/049-postgres-connection-layer.md`

## User stories addressed

Every tenant-scoped API endpoint depends on these. The repo conventions established here are inherited by 052 and 053.
