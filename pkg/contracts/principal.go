package contracts

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Principal is the authenticated identity attached to every authorized
// request. It is produced by the WorkOS JWT verifier (internal/auth) and
// consumed by the auth middleware (issue 031), repositories, and handlers.
//
// Fields:
//
//   - UserID:  WorkOS `sub` claim, must be a UUID. Joins to users.id.
//   - TenantID: the locked `tenant_id` claim (CLAUDE.md invariant). Drives
//     SET LOCAL app.current_tenant for RLS.
//   - Roles:   optional WorkOS roles claim (`roles`) — empty slice if absent.
//   - TokenID: the JWT `jti` claim — used for revocation and audit logging.
//   - TokenType: optional WorkOS `token_type` claim — one of "cli" or "daemon"
//     for v1. Empty string when absent. Consumed by the rate-limit middleware
//     (issue 032) to pick the per-token sliding-window bucket size
//     (100/min CLI, 600/min daemon per ARCHITECTURE.md §5).
//
// Principal is immutable. Callers that need to derive a modified copy should
// construct a new value rather than mutating fields.
type Principal struct {
	UserID    uuid.UUID
	TenantID  uuid.UUID
	Roles     []string
	TokenID   string
	TokenType string
}

// principalCtxKey is unexported so external packages cannot collide with
// our context value. Idiomatic Go context-key pattern.
type principalCtxKey struct{}

// WithPrincipal returns a new context carrying p. Use this in the auth
// middleware after a successful VerifyToken call.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFromContext retrieves the principal attached by the auth
// middleware. Returns (Principal{}, false) if no principal is present.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// ErrUnauthenticated is returned by RequireAuth when no principal has been
// installed on the context. Handlers may translate this to HTTP 401.
var ErrUnauthenticated = errors.New("contracts: no authenticated principal on context")

// RequireAuth is a convenience for handlers that need a principal: it either
// returns one, or returns ErrUnauthenticated. The middleware layer is
// expected to short-circuit unauthenticated requests before they reach a
// handler, but RequireAuth is a defensive backstop for internal callers.
func RequireAuth(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	return p, nil
}
