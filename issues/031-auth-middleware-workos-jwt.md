---
type: AFK
depends-on:
  - 029-middleware-scaffold
  - 056-workos-auth-jwt-verifier
---

## Parent PRD

`ARCHITECTURE.md` §5 "Auth" + §9 Step 4: "Auth middleware (WorkOS JWT, JWKS cache 1h)." `CLAUDE.md` "Locked invariants" — tokens carry `tenant_id` claim.

## What to build

`internal/api/middleware/auth.go` — verifies inbound bearer tokens against WorkOS-issued JWTs, populates `context.Context` with the authenticated principal, rejects malformed/expired/wrong-issuer tokens with 401.

Concretely:

1. JWKS fetcher with **1h cache** (sliding TTL acceptable). On cache miss, fetch from `WORKOS_JWKS_URL`; on fetch failure, serve from stale cache for an additional 10m grace window; if no cache at all and fetch fails, return 503 with `{"error":"auth_unavailable"}` (do NOT treat as 401 — distinguishable upstream).
2. Token parse + verification with `github.com/lestrrat-go/jwx/v2` (or `github.com/golang-jwt/jwt/v5`; pick one and record in DECISIONS.md). Validate: signature against the JWKS, `iss` matches `WORKOS_ISSUER`, `aud` matches `WORKOS_AUDIENCE`, `exp` is in the future (5s clock skew tolerance), `nbf` is in the past, `tenant_id` claim is a valid UUID, `sub` claim is a valid UUID.
3. Principal struct (`internal/api/principal.go`): `{ UserID uuid.UUID; TenantID uuid.UUID; Roles []string; TokenID string }`. Inject via `context.WithValue` using a private key type.
4. Helpers: `PrincipalFromContext(ctx) (Principal, bool)`, `RequireAuth(ctx) Principal` (panics if missing — for handlers that know they're behind auth).
5. Whitelist `/health` from the auth chain (registered before the middleware).

## Acceptance criteria

- [ ] JWKS cache TTL is 1h; sliding window verified in tests
- [ ] Stale-while-revalidate grace window of 10m on fetch failure
- [ ] Invalid tokens → 401 with `WWW-Authenticate: Bearer realm="iter"` header and a generic body (do not leak the failure reason to the client beyond "invalid_token" / "expired_token")
- [ ] Missing tenant_id or sub claim → 401, logged as a security event (`auth_missing_claim`)
- [ ] JWT library choice recorded in `DECISIONS.md`
- [ ] Tests cover: valid token, expired, wrong issuer, wrong audience, bad signature, missing tenant_id, JWKS unreachable + warm cache (200), JWKS unreachable + cold cache (503)
- [ ] No real HTTP calls in tests — JWKS is mocked or injected via an interface
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/029-middleware-scaffold.md`
- Blocked by Step 3 storage-layer baseline — needs env-var loading conventions for `WORKOS_*` keys

## User stories addressed

Every authenticated user story — Adam authenticating from CLI, dashboard fetches, etc.
