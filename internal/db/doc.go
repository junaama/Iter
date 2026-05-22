// Package db owns the Postgres connection layer and the integration tests
// that enforce data-layer invariants — tenant isolation (RLS) and cascade
// delete chains. See migrations/0001_initial.sql for the canonical schema.
package db
