package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Iter-issued session JWT contract.
//
// The Mac app (and CLI, in a future slice) signs in via WorkOS device-code,
// exchanges the WorkOS access token at POST /v1/auth/session, and receives
// an Iter-issued HS256 JWT keyed by ITER_JWT_SECRET. The auth middleware
// then validates every subsequent request against THIS token, not the raw
// WorkOS token — because WorkOS access tokens carry `sub = "user_01KS..."`
// (a WorkOS prefixed id) with no `tenant_id` claim, neither of which the
// downstream RLS-scoped repos can use as Postgres UUIDs.
//
// Algorithm choice (HS256): a single-server local-dev setup; no third party
// needs to verify these. RS256+JWKS is the obvious graduation step but is
// premature today.
//
// Claims:
//
//   - iss: IterIssuer ("iter")
//   - aud: IterAudience ("iter-api")
//   - sub: Iter user UUID (joins users.id)
//   - tenant_id: Iter tenant UUID (drives SET LOCAL app.current_tenant)
//   - exp: 24h from issuance (DefaultTTL)
//   - iat / nbf: issuance time
//   - jti: random UUID — fed to the rate-limit middleware as the bucket key
//   - roles: optional []string
//   - token_type: "session" (matches the existing optional claim the
//     verifier already understands; rate-limit middleware picks a bucket
//     by token_type when present)
const (
	IterIssuer    = "iter"
	IterAudience  = "iter-api"
	IterTokenType = "session"

	// DefaultIterTTL is the lifetime of a freshly minted Iter session
	// JWT. Long enough that a Mac app doesn't refresh on every screen
	// (the WorkOS access token expires faster, so the actual cap is
	// usually WorkOS's lifetime + refresh cycle); short enough that a
	// stolen token has a bounded blast radius.
	DefaultIterTTL = 24 * time.Hour
)

// IterTokenClaims is what callers pass to IterSigner.Sign. Roles is
// optional; the rest are required. The exchange handler (issue: this
// slice) fills these from the upserted users / tenants rows.
type IterTokenClaims struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Roles    []string
}

// IterSigner mints Iter-issued session JWTs.
//
// The secret is captured at construction; rotating means restarting the
// server. v1 does not implement per-token revocation — short TTL is the
// only blast-radius limit. A revocation list lives on the roadmap but
// is out of scope for the WorkOS→Iter exchange wiring.
type IterSigner struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewIterSigner constructs a signer. secret must be a non-empty random
// string; an empty value is a programming error (boot is expected to
// fail loudly before reaching this constructor).
func NewIterSigner(secret string) (*IterSigner, error) {
	if secret == "" {
		return nil, errors.New("auth: NewIterSigner: secret is required")
	}
	return &IterSigner{
		secret: []byte(secret),
		ttl:    DefaultIterTTL,
		now:    time.Now,
	}, nil
}

// WithTTL overrides the default 24h token lifetime. Used by tests.
func (s *IterSigner) WithTTL(ttl time.Duration) *IterSigner {
	clone := *s
	clone.ttl = ttl
	return &clone
}

// WithClock overrides time.Now. Used by tests.
func (s *IterSigner) WithClock(now func() time.Time) *IterSigner {
	clone := *s
	clone.now = now
	return &clone
}

// TTL returns the configured token lifetime. Exposed so the exchange
// handler can echo it back as `expires_in` in the response body.
func (s *IterSigner) TTL() time.Duration { return s.ttl }

// Sign returns a signed Iter session JWT carrying claims. The returned
// string is suitable for a Bearer header on subsequent requests.
func (s *IterSigner) Sign(claims IterTokenClaims) (string, error) {
	if claims.UserID == uuid.Nil {
		return "", errors.New("auth: IterSigner.Sign: UserID required")
	}
	if claims.TenantID == uuid.Nil {
		return "", errors.New("auth: IterSigner.Sign: TenantID required")
	}

	now := s.now()
	b := jwt.NewBuilder().
		Issuer(IterIssuer).
		Audience([]string{IterAudience}).
		Subject(claims.UserID.String()).
		IssuedAt(now).
		NotBefore(now).
		Expiration(now.Add(s.ttl)).
		JwtID(uuid.NewString()).
		Claim("tenant_id", claims.TenantID.String()).
		Claim("token_type", IterTokenType)

	if len(claims.Roles) > 0 {
		b = b.Claim("roles", append([]string(nil), claims.Roles...))
	}

	tok, err := b.Build()
	if err != nil {
		return "", fmt.Errorf("auth: IterSigner.Sign build: %w", err)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.HS256, s.secret))
	if err != nil {
		return "", fmt.Errorf("auth: IterSigner.Sign: %w", err)
	}
	return string(signed), nil
}

// IterVerifier verifies Iter-issued session JWTs. Implements the same
// Verify(ctx, raw) signature as *Verifier so the auth middleware
// (internal/api/middleware) and WS gateway can consume either via the
// TokenVerifier interface in interface.go.
//
// HS256 is symmetric: the verifier holds the same secret as the signer.
// In v1 they're literally the same process; the abstraction exists so a
// future split (e.g. signer-only minting service) doesn't require a
// callsite refactor.
type IterVerifier struct {
	secret   []byte
	issuer   string
	audience string
	leeway   time.Duration
	now      func() time.Time
}

// NewIterVerifier constructs a verifier. Mirrors NewIterSigner — empty
// secret is a programming error caught at boot.
func NewIterVerifier(secret string) (*IterVerifier, error) {
	if secret == "" {
		return nil, errors.New("auth: NewIterVerifier: secret is required")
	}
	return &IterVerifier{
		secret:   []byte(secret),
		issuer:   IterIssuer,
		audience: IterAudience,
		leeway:   defaultLeeway,
		now:      time.Now,
	}, nil
}

// WithClock injects a clock. Used by tests.
func (v *IterVerifier) WithClock(now func() time.Time) *IterVerifier {
	clone := *v
	clone.now = now
	return &clone
}

// Verify parses, validates, and extracts a Principal from an Iter
// session JWT. Maps to the same sentinel errors as *Verifier so the
// middleware's error-classifier doesn't have to branch on token kind.
func (v *IterVerifier) Verify(_ context.Context, raw string) (contracts.Principal, error) {
	if raw == "" {
		return contracts.Principal{}, ErrMalformed
	}
	opts := []jwt.ParseOption{
		jwt.WithKey(jwa.HS256, v.secret),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithAcceptableSkew(v.leeway),
		jwt.WithClock(jwtClock{now: v.now}),
	}
	tok, err := jwt.Parse([]byte(raw), opts...)
	if err != nil {
		return contracts.Principal{}, classifyJWTError(err)
	}
	return extractPrincipal(tok)
}

// TokenVerifier is the contract the auth middleware and WS gateway hold
// against any JWT verifier. Both *Verifier (WorkOS RS256 via JWKS) and
// *IterVerifier (Iter HS256) satisfy it; deps.Auth holds whichever the
// boot path picked.
type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (contracts.Principal, error)
}

// Compile-time interface assertions. Catches a future refactor that
// drifts either Verify signature out of alignment.
var (
	_ TokenVerifier = (*Verifier)(nil)
	_ TokenVerifier = (*IterVerifier)(nil)
)
