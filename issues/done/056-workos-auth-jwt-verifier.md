---
type: HITL+AFK
depends-on:
  - 007-go-module-skeleton
---

# Mixed HITL + AFK

The WorkOS dashboard work (creating OIDC apps, configuring redirect URIs) is HITL. The Go code (`internal/auth` JWT verifier, JWKS cache, claim extractor) is AFK.

## Parent PRD

`ARCHITECTURE.md` §5 "Auth"; §9 Step 3 ("WorkOS OIDC app"); §9 Step 4 ("Auth middleware (WorkOS JWT, JWKS cache 1h)"). `CLAUDE.md` "Locked invariants": tokens carry `tenant_id` claim, stored in macOS Keychain on the daemon; device-code OAuth flow on `iter login`; SSO/SAML available when an enterprise needs it.

## What to build

The WorkOS OIDC apps for dev/staging/prod, plus the Go primitives that verify the JWTs they mint. The middleware that *uses* these primitives lands in Step 4 — this slice is the verifier + claim extractor in isolation.

### HITL — WorkOS dashboard

1. Create three WorkOS OIDC applications: `iter-dev`, `iter-staging`, `iter-prod`. Record client IDs and API keys in `deploy.md` (or in a Railway secret; `deploy.md` already lists `WORKOS_CLIENT_ID` / `WORKOS_API_KEY`).
2. Configure redirect URIs per environment: `https://iter.dev/auth/callback` (prod), `https://staging.iter.dev/auth/callback` (staging), `http://localhost:8080/auth/callback` (dev).
3. Enable the **device-code grant** for each app (required for the CLI/daemon flow per `DECISIONS.md` Phase 5).
4. SAML/SSO connections deferred to first enterprise contract — note in `DECISIONS.md` if not already.
5. Capture the JWKS URL per environment; record in `deploy.md`.

### AFK — Go primitives in `internal/auth`

1. **JWKS cache**: `auth.JWKSCache` fetches the WorkOS JWKS endpoint, caches keys for 1h per `ARCHITECTURE.md` §9 Step 4. Refreshes on cache miss or `kid` not found.
2. **Verifier**: `auth.VerifyToken(ctx, raw string) (Claims, error)` validates signature, `exp`, `nbf`, `iss`, `aud`. Returns a typed `Claims` struct including `Subject` (`sub`), `TenantID`, `UserID`, `Email`, `Issuer`, `IssuedAt`, `ExpiresAt`.
3. **Claim extraction**: `Claims.TenantID()` is the only sanctioned way to derive the tenant for `db.WithTenant`. A token without a `tenant_id` claim is rejected.
4. **Fixture tests** (no live WorkOS calls):
   - Well-formed token signed by a test keypair → verifies.
   - Expired token → `auth.ErrExpired`.
   - Wrong issuer / audience → `auth.ErrInvalidClaims`.
   - Malformed token (not three segments, bad base64) → `auth.ErrMalformed`.
   - Missing `tenant_id` claim → `auth.ErrMissingTenant`.
   - JWKS cache: first call fetches, second call within 1h reuses, third call after 1h re-fetches.
   - Unknown `kid` triggers a refresh.

Do NOT wire the auth middleware (`internal/api/middleware/auth.go`) in this slice — that's Step 4. This slice is the pure verifier so it can be tested in isolation.

## Acceptance criteria

### HITL

- [ ] Three WorkOS OIDC apps created (`iter-dev`, `iter-staging`, `iter-prod`)
- [ ] Device-code grant enabled per app
- [ ] Redirect URIs configured per env
- [ ] Client IDs / API keys / JWKS URLs recorded in Railway env vars and `deploy.md`

### AFK

- [ ] `internal/auth.JWKSCache` with 1h TTL + `kid`-miss refresh
- [ ] `internal/auth.VerifyToken` returns typed `Claims`
- [ ] `Claims.TenantID()` is the sanctioned extractor
- [ ] Errors: `ErrExpired`, `ErrInvalidClaims`, `ErrMalformed`, `ErrMissingTenant`
- [ ] Fixture tests cover: valid, expired, wrong issuer, malformed, missing tenant, cache TTL, kid-miss refresh
- [ ] `make test` + `make lint` pass

## Status

AFK scope **complete**. HITL scope **deferred** — see "HITL items remaining" below.

### AFK delivered

- `internal/auth/verifier.go` — `Verifier` with JWKS cache (1h fresh TTL, 10m stale-while-revalidate window, kid-miss synchronous refresh).
- `internal/auth/verifier_test.go` — table-driven coverage for: valid, expired, wrong issuer, wrong audience, missing tenant_id, non-UUID tenant_id, non-UUID sub, malformed (not 3 segments), empty, bad signature; plus dedicated tests for warm-cache + JWKS-down, cold-cache + JWKS-down (`ErrAuthUnavailable`), and cache TTL refresh-after-expiry.
- `internal/auth/doc.go` — package overview.
- `pkg/contracts/principal.go` — `Principal` struct (`UserID`, `TenantID`, `Roles`, `TokenID`); `WithPrincipal`, `PrincipalFromContext`, `RequireAuth` helpers; `ErrUnauthenticated` sentinel.
- `DECISIONS.md` — three new entries: JWT library choice (`lestrrat-go/jwx/v2`), claim contract (tenant_id + sub must be UUIDs), cache failure-mode policy (degraded auth preferred over no auth).
- `go.mod` — added `github.com/lestrrat-go/jwx/v2` and `github.com/google/uuid`.
- `make test` and `make lint` both green.

### HITL items remaining

The middleware that *uses* this verifier is issue 031 (Step 4) — explicitly out of scope here. The remaining HITL items, blocking only the **end-to-end auth flow** (not this slice), are:

- [ ] Create three WorkOS OIDC applications: `iter-dev`, `iter-staging`, `iter-prod`. Record client IDs and API keys in Railway env (`WORKOS_CLIENT_ID`, `WORKOS_API_KEY`).
- [ ] Configure redirect URIs per env: `https://iter.dev/auth/callback` (prod), `https://staging.iter.dev/auth/callback` (staging), `http://localhost:8080/auth/callback` (dev).
- [ ] Enable the device-code grant for each app (CLI/daemon flow).
- [ ] Capture JWKS URL per env; set as `WORKOS_JWKS_URL` in Railway.
- [ ] Set `WORKOS_ISSUER` and `WORKOS_AUDIENCE` env vars per env (the verifier's `VerifierConfig` reads these at boot).
- [ ] SAML/SSO connections deferred to first enterprise contract — already noted in `DECISIONS.md` Phase 5.

When the HITL pieces land, wire them into `internal/app.Deps` and into `auth.NewVerifier(...)` in `cmd/server`. The verifier itself needs no further code changes to consume them.

## Blocked by

- Blocked by `issues/007-go-module-skeleton.md` (resolved)

## User stories addressed

Every authenticated API call, the `iter login` device-code flow, the dashboard session — all run through `VerifyToken`. The "former teammate display name" and "user account deletion" failure-mode behaviors (§7) sit on top of stable claim semantics.
