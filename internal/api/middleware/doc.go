// Package middleware contains the HTTP middleware stack composed by
// internal/api/router.go.
//
// Per ARCHITECTURE.md §9 Step 4 the stack order is:
//
//	request_id → logger → recover → auth → tenant_context → rate_limit → idempotency
//
// Issue 029 ships the first three. Auth (031), idempotency (033),
// rate_limit (032), and tenant_context (034) slot in afterward.
//
// DECISIONS.md "HTTP router (issue 028)" forbids importing
// github.com/go-chi/chi/v5/middleware — every middleware concern lives
// here so the router lock-in stays low.
package middleware
