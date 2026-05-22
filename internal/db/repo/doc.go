// Package repo houses the table-scoped repository functions for the v1
// Postgres schema (migrations/0001_initial.sql). It is the single layer
// between the request path and raw SQL.
//
// # Conventions
//
//   - One file per table: tenants.go, users.go, tenant_users.go,
//     sessions.go, session_events.go (this file group is issue 051;
//     issues 052 and 053 add the rest).
//
//   - Every repository function takes a pgx.Tx as its first argument
//     (after context.Context). Callers obtain the tx from db.WithTenant
//     or db.WithBatch — this makes it impossible to bypass RLS by
//     accident because *pgxpool.Pool will not satisfy the signature.
//
//   - Standard verb shapes:
//     InsertX(ctx, tx, in) (out, err) — returns the persisted row or its id.
//     GetX(ctx, tx, id)    (out, err) — returns pgx.ErrNoRows when missing.
//     ListX(ctx, tx, filt) ([]out, err)
//     UpdateX(ctx, tx, ...) error
//     DeleteX(ctx, tx, id) error
//     Not every table needs all five.
//
//   - Errors wrap with fmt.Errorf("repo.<table>.<op>: %w", err). The
//     canonical not-found is pgx.ErrNoRows; callers test it with
//     errors.Is(err, pgx.ErrNoRows).
//
//   - Row types live in this package (not pkg/contracts). pkg/contracts
//     is the wire layer (daemon/CLI/dashboard/webhook). Repo types are
//     the storage layer; the API layer maps between them.
//
//   - Time columns deserialize as time.Time (UTC). The schema uses
//     timestamptz everywhere; pgx returns time.Time with the database's
//     location, which is UTC for the v1 cluster.
//
// # Tenant-scoped tables
//
// sessions and session_events both carry a tenant_id and an RLS
// tenant_isolation policy. Their repo functions assume the tx is
// already inside a db.WithTenant block; they do not re-issue
// SET LOCAL app.current_tenant. The repo never reads tenant_id from
// untrusted input — it reads it from the row or trusts that RLS
// hides the wrong-tenant rows.
//
// # Tenancy tables (tenants, users, tenant_users)
//
// These have no RLS policy at v1: tenants and users are global, and
// tenant_users is the membership table (access mediation happens via
// join, not RLS — see rls_test.go's tolerance comment). Their repo
// functions run inside db.WithBatch for admin paths (tenant
// provisioning, user signup) or inside any tx that wants to read
// membership. Callers are responsible for authorization.
package repo
