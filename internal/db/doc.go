// Package db owns the Postgres connection layer and the integration tests
// that enforce data-layer invariants — tenant isolation (RLS) and cascade
// delete chains. See migrations/0001_initial.sql for the canonical schema.
//
// # PgBouncer transaction-mode caveat
//
// Production traffic flows daemon → server → PgBouncer (transaction mode)
// → Postgres. Transaction mode rebinds a server connection to a different
// client between transactions, which invalidates any server-side state
// scoped to a session — including named prepared statements.
//
// pgx's default DefaultQueryExecMode is QueryExecModeCacheStatement,
// which creates and caches such prepared statements. Using it through
// PgBouncer in transaction mode produces errors like "prepared statement
// already exists" or "prepared statement does not exist" depending on
// timing. NewPool therefore forces QueryExecModeCacheDescribe at the
// connection layer. cache_describe caches column-type descriptions on
// the pgx side (per pgx.Conn, client-side only) and sends each query as
// an unnamed extended-protocol round-trip that PgBouncer can route
// transparently.
//
// Callers MUST NOT downgrade to QueryExecModeCacheStatement on
// individual queries. The package documentation in pool.go reiterates
// this; the integration tests don't enforce it because the failure
// mode only manifests under PgBouncer, not against bare Postgres.
//
// # Tenant isolation
//
// Every request-path query MUST run inside db.WithTenant (which opens a
// transaction and sets app.current_tenant via SET LOCAL). Calling pool
// methods directly bypasses RLS and is a security bug. The matching
// BYPASSRLS path for cross-tenant batch work is db.WithBatch, which
// requires a separate iter_batch pool built from DATABASE_URL_BATCH.
package db
