---
type: AFK
depends-on:
  - 031-auth-middleware-workos-jwt
  - 049-postgres-connection-layer
---

## Parent PRD

`CLAUDE.md` "Locked invariants" — RLS enforced via per-transaction `SET LOCAL app.current_tenant = '<uuid>'`. `ARCHITECTURE.md` §9 Step 4: "Middleware stack: … auth → tenant context → rate limit …"

## What to build

`internal/api/middleware/tenant.go` — runs immediately after auth (016). Reads `Principal.TenantID` from the request context, opens a transaction-scoped pgx connection (or annotates the context for the repository layer), and ensures every DB query inside the handler runs with `SET LOCAL app.current_tenant = '<tenant_id>'` set.

Two viable implementations — pick one and document in `DECISIONS.md`:

**Option A (per-request tx, simpler):** middleware opens a `pgx.Tx`, sets the GUC, stashes the tx in the request context. Handlers/repos use the tx via `db.TxFromContext(ctx)`. Commit on 2xx response, rollback otherwise. Drawback: every request holds a connection for its full duration — not great under load.

**Option B (per-statement helper, lower load):** middleware stashes `tenant_id` in the request context only. The repository layer's `db.Querier(ctx).Query(...)` helper opens a short-lived tx around each statement (or batch) and applies `SET LOCAL` inside it. Drawback: more complex repo layer; consistency model nuances if a handler runs >1 query.

Recommended: **A** for v1 (correctness > load; we have headroom). Migrate to B if pgxpool contention shows up in load tests (Step 6).

Plus:

- Whitelist `/health`, webhooks (no tenant context — see 018), and any future public endpoints.
- If the principal lacks tenant_id (shouldn't happen post-016 but defensive) → 401.

## Acceptance criteria

- [ ] Implementation choice (A vs. B) recorded in `DECISIONS.md` with rationale
- [ ] Inside any handler downstream of this middleware, `SELECT current_setting('app.current_tenant')` returns the authenticated tenant_id
- [ ] Transaction commits on 2xx, rolls back on 4xx/5xx (test with a handler that returns each)
- [ ] Whitelist verified: `/health` and webhooks run WITHOUT a tx in context
- [ ] Tenant_id propagates through the `internal/db` repository functions without manual plumbing (the helper reads from `ctx`)
- [ ] Tests cover: happy path, rollback on 500, whitelist bypass, missing-tenant_id → 401
- [ ] Integration test (`-tags=integration`) extends `internal/db/rls_test.go` to assert a real handler under this middleware respects RLS end-to-end
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/031-auth-middleware-workos-jwt.md`
- Blocked by Step 3 storage-layer baseline — needs pgxpool + the repository pattern's context-aware Querier helper

## User stories addressed

Locked invariant. If this breaks, every multi-tenant story is broken.
