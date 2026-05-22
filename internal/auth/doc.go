// Package auth will host the WorkOS device-code flow for the CLI/daemon and
// the JWT verifier for the cloud server. Tokens carry a tenant_id claim that
// the verifier exposes for the tenant_context middleware (internal/api) to
// stamp onto the per-request RLS GUC.
//
// Intentionally empty at issue 048 — this slice only stamps the §9 Step 3
// repository layout on disk. The JWT verifier lands in issue 056; the
// device-code flow lives in the macOS daemon and lands later.
package auth
