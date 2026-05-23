package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Sentinel errors. Keep these stable — middleware (issue 031) pattern-matches
// on them to map to HTTP status codes.
var (
	ErrExpired         = errors.New("auth: token expired")
	ErrNotYetValid     = errors.New("auth: token not yet valid (nbf)")
	ErrInvalidClaims   = errors.New("auth: invalid claims (iss/aud)")
	ErrMalformed       = errors.New("auth: malformed token")
	ErrBadSignature    = errors.New("auth: bad signature")
	ErrMissingTenant   = errors.New("auth: missing or non-UUID tenant_id claim")
	ErrMissingSubject  = errors.New("auth: missing or non-UUID sub claim")
	ErrAuthUnavailable = errors.New("auth: JWKS unavailable and cache is cold")
)

// Cache TTL contract (per ARCHITECTURE.md §9 Step 4 "JWKS cache 1h").
// The sliding TTL is the "fresh" window; past that, we still serve the cache
// for staleWindow while a background refresh is in flight.
const (
	defaultFreshTTL    = 1 * time.Hour
	defaultStaleWindow = 10 * time.Minute
	defaultLeeway      = 30 * time.Second
)

// JWKSFetcher abstracts the network call so tests can supply a fake without
// standing up an HTTP server. Production wires this to jwk.Fetch.
type JWKSFetcher func(ctx context.Context, url string) (jwk.Set, error)

// VerifierConfig is the static configuration captured from env at boot.
type VerifierConfig struct {
	JWKSURL  string
	Issuer   string
	Audience string

	// Optional overrides — tests use these; production should leave them zero
	// to pick up the defaults above.
	FreshTTL    time.Duration
	StaleWindow time.Duration
	Leeway      time.Duration
	Now         func() time.Time // injectable clock for tests
	Fetch       JWKSFetcher      // injectable fetcher for tests
}

// Verifier verifies WorkOS-issued JWTs against a cached JWKS.
//
// The cache lifecycle:
//
//  1. First Verify() call → fetch JWKS, store with fetchedAt = now.
//  2. Subsequent calls within FreshTTL → reuse cached set (no network).
//  3. Calls between FreshTTL and FreshTTL+StaleWindow → reuse cached set AND
//     trigger an async refresh (stale-while-revalidate).
//  4. Calls after FreshTTL+StaleWindow → block on a synchronous refresh; if
//     it fails AND the cache is empty, return ErrAuthUnavailable. If a stale
//     cache exists, we still attempt the synchronous refresh first and only
//     fall back to the stale set on network failure.
//  5. Token references an unknown `kid` → force a synchronous refresh
//     regardless of TTL, then re-attempt key lookup once.
type Verifier struct {
	cfg VerifierConfig

	mu        sync.RWMutex
	cached    jwk.Set
	fetchedAt time.Time

	// refreshMu serializes refresh attempts so a thundering herd doesn't
	// hit WorkOS with N parallel requests on every kid-miss.
	refreshMu sync.Mutex
}

// NewVerifier constructs a Verifier and fills in default behavior for any
// zero-valued config fields. It does NOT fetch the JWKS — the first Verify
// call triggers the initial fetch.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if cfg.JWKSURL == "" {
		return nil, errors.New("auth: VerifierConfig.JWKSURL is required")
	}
	if cfg.Issuer == "" {
		return nil, errors.New("auth: VerifierConfig.Issuer is required")
	}
	// Audience is OPTIONAL. WorkOS AuthKit-issued JWTs do not include an
	// `aud` claim (verified against
	// https://workos.com/docs/reference/authkit/session-tokens/jwks —
	// the example payload has `client_id` instead). When Audience is
	// empty we skip the WithAudience option below, and verification
	// still pins to the configured Issuer + the JWKS URL (which itself
	// is keyed by client_id, so a valid signature implicitly proves
	// the token was issued for our application). Leaving this field
	// settable lets non-AuthKit OIDC providers (or a future WorkOS
	// change) opt back in without code edits.
	if cfg.FreshTTL <= 0 {
		cfg.FreshTTL = defaultFreshTTL
	}
	if cfg.StaleWindow <= 0 {
		cfg.StaleWindow = defaultStaleWindow
	}
	if cfg.Leeway <= 0 {
		cfg.Leeway = defaultLeeway
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Fetch == nil {
		cfg.Fetch = func(ctx context.Context, url string) (jwk.Set, error) {
			return jwk.Fetch(ctx, url)
		}
	}
	return &Verifier{cfg: cfg}, nil
}

// snapshot returns the cached set + its fetchedAt under read lock.
func (v *Verifier) snapshot() (jwk.Set, time.Time) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.cached, v.fetchedAt
}

// store replaces the cached set under write lock.
func (v *Verifier) store(set jwk.Set, at time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cached = set
	v.fetchedAt = at
}

// refresh performs a synchronous JWKS fetch and updates the cache on success.
// On failure, the existing cache is left untouched and the network error is
// returned to the caller.
func (v *Verifier) refresh(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	set, err := v.cfg.Fetch(ctx, v.cfg.JWKSURL)
	if err != nil {
		return fmt.Errorf("auth: JWKS fetch: %w", err)
	}
	v.store(set, v.cfg.Now())
	return nil
}

// keys returns a usable JWKS for verification. It applies the cache lifecycle
// described on the Verifier doc comment. The returned set may be stale (within
// the stale window) when the network is down; callers should treat the
// returned set as authoritative for this verification attempt.
func (v *Verifier) keys(ctx context.Context) (jwk.Set, error) {
	cached, fetchedAt := v.snapshot()
	now := v.cfg.Now()

	// Cold cache → synchronous fetch.
	if cached == nil {
		if err := v.refresh(ctx); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAuthUnavailable, err)
		}
		set, _ := v.snapshot()
		return set, nil
	}

	age := now.Sub(fetchedAt)

	// Fresh: serve cache directly.
	if age <= v.cfg.FreshTTL {
		return cached, nil
	}

	// Stale window: serve cache, kick off background refresh.
	if age <= v.cfg.FreshTTL+v.cfg.StaleWindow {
		go func() {
			// Use a fresh context so a cancelled request doesn't kill
			// the refresh. Bound it so a hung WorkOS endpoint can't pile
			// up goroutines.
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = v.refresh(bgCtx)
		}()
		return cached, nil
	}

	// Beyond stale: try synchronous refresh; fall back to stale cache if the
	// fetch fails. We never return ErrAuthUnavailable when we still have
	// *some* cached keys — the verifier prefers degraded auth over no auth.
	if err := v.refresh(ctx); err != nil {
		return cached, nil
	}
	set, _ := v.snapshot()
	return set, nil
}

// VerifyRaw parses + signature-verifies a WorkOS-issued JWT and returns
// the underlying jwt.Token. Unlike Verify, it does NOT require the
// `sub` or `tenant_id` claims to be Postgres UUIDs — WorkOS access
// tokens carry `sub = "user_01KS..."` (a prefixed WorkOS id) and no
// tenant_id at all. Used by the token-exchange handler at
// POST /v1/auth/session.
//
// The returned token still has issuer / exp / nbf / signature checks
// applied; only the iter-specific claim shape is skipped. Callers must
// not pass the resulting token to extractPrincipal — fish out the
// WorkOS sub via tok.Subject() and look up (or mint) the Iter user
// row keyed by that opaque string.
func (v *Verifier) VerifyRaw(ctx context.Context, raw string) (jwt.Token, error) {
	if raw == "" {
		return nil, ErrMalformed
	}
	msg, err := jws.Parse([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	sigs := msg.Signatures()
	if len(sigs) == 0 {
		return nil, ErrMalformed
	}
	kid := sigs[0].ProtectedHeaders().KeyID()

	set, err := v.keys(ctx)
	if err != nil {
		return nil, err
	}
	if kid != "" {
		if _, ok := set.LookupKeyID(kid); !ok {
			if rerr := v.refresh(ctx); rerr == nil {
				set, _ = v.snapshot()
			}
		}
	}

	opts := []jwt.ParseOption{
		jwt.WithKeySet(set),
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithAcceptableSkew(v.cfg.Leeway),
		jwt.WithClock(jwtClock{now: v.cfg.Now}),
	}
	if v.cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(v.cfg.Audience))
	}
	tok, err := jwt.Parse([]byte(raw), opts...)
	if err != nil {
		return nil, classifyJWTError(err)
	}
	return tok, nil
}

// Verify parses, validates, and extracts claims from a WorkOS-issued JWT.
//
// On success it returns a fully-populated contracts.Principal. On failure it
// returns one of the sentinel errors at the top of this file, wrapped with
// %w so callers can use errors.Is.
func (v *Verifier) Verify(ctx context.Context, raw string) (contracts.Principal, error) {
	if raw == "" {
		return contracts.Principal{}, ErrMalformed
	}

	// Parse the JWS header first so we can look up the right key by `kid`
	// before incurring the JWKS round-trip on a malformed token.
	msg, err := jws.Parse([]byte(raw))
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	sigs := msg.Signatures()
	if len(sigs) == 0 {
		return contracts.Principal{}, ErrMalformed
	}
	kid := sigs[0].ProtectedHeaders().KeyID()

	set, err := v.keys(ctx)
	if err != nil {
		return contracts.Principal{}, err
	}

	// Kid-miss → force refresh once.
	if kid != "" {
		if _, ok := set.LookupKeyID(kid); !ok {
			if rerr := v.refresh(ctx); rerr == nil {
				set, _ = v.snapshot()
			}
		}
	}

	// jwt.Parse handles signature, exp, nbf, iss, aud validation. We pass the
	// JWKS via WithKeySet so it picks the matching kid; the leeway absorbs
	// small clock skew between WorkOS and us. WithAudience is conditional:
	// AuthKit-issued JWTs don't include `aud`, and forcing the check would
	// reject every real token.
	opts := []jwt.ParseOption{
		jwt.WithKeySet(set),
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithAcceptableSkew(v.cfg.Leeway),
		jwt.WithClock(jwtClock{now: v.cfg.Now}),
	}
	if v.cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(v.cfg.Audience))
	}
	tok, err := jwt.Parse([]byte(raw), opts...)
	if err != nil {
		return contracts.Principal{}, classifyJWTError(err)
	}

	return extractPrincipal(tok)
}

// classifyJWTError maps jwx's error sentinels to our public sentinel errors.
// jwx returns typed errors via jwt.ErrTokenExpired() etc.; we rewrap so
// downstream code only depends on the contracts in this package.
func classifyJWTError(err error) error {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired()):
		return fmt.Errorf("%w: %v", ErrExpired, err)
	case errors.Is(err, jwt.ErrTokenNotYetValid()):
		return fmt.Errorf("%w: %v", ErrNotYetValid, err)
	case errors.Is(err, jwt.ErrInvalidIssuer()),
		errors.Is(err, jwt.ErrInvalidAudience()):
		return fmt.Errorf("%w: %v", ErrInvalidClaims, err)
	}
	// jws.Verify failures bubble up here too; treat as bad signature.
	return fmt.Errorf("%w: %v", ErrBadSignature, err)
}

// extractPrincipal pulls the iter-specific claims off a verified token and
// builds a Principal. The tenant_id and sub claims MUST be UUIDs — anything
// else is a contract violation worth rejecting.
func extractPrincipal(tok jwt.Token) (contracts.Principal, error) {
	subRaw := tok.Subject()
	if subRaw == "" {
		return contracts.Principal{}, ErrMissingSubject
	}
	userID, err := uuid.Parse(subRaw)
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("%w: %v", ErrMissingSubject, err)
	}

	tenantRaw, ok := tok.Get("tenant_id")
	if !ok {
		return contracts.Principal{}, ErrMissingTenant
	}
	tenantStr, ok := tenantRaw.(string)
	if !ok || tenantStr == "" {
		return contracts.Principal{}, ErrMissingTenant
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("%w: %v", ErrMissingTenant, err)
	}

	// roles is optional. Accept []string or []any (JSON decoding yields []any).
	var roles []string
	if rolesRaw, ok := tok.Get("roles"); ok {
		switch v := rolesRaw.(type) {
		case []string:
			roles = append(roles, v...)
		case []any:
			for _, r := range v {
				if s, ok := r.(string); ok {
					roles = append(roles, s)
				}
			}
		}
	}

	// token_type is optional. Per the issue 032 contract, "cli" and
	// "daemon" are the canonical values; any other string is preserved
	// verbatim so future tiers (e.g. "ci") don't need a verifier change.
	// Non-string claim values are dropped silently — the rate-limit
	// middleware treats absence as the conservative default (100/min).
	var tokenType string
	if ttRaw, ok := tok.Get("token_type"); ok {
		if s, ok := ttRaw.(string); ok {
			tokenType = s
		}
	}

	return contracts.Principal{
		UserID:    userID,
		TenantID:  tenantID,
		Roles:     roles,
		TokenID:   tok.JwtID(),
		TokenType: tokenType,
	}, nil
}

// jwtClock adapts a func() time.Time into the jwt.Clock interface so tests
// can drive time without monkey-patching time.Now.
type jwtClock struct {
	now func() time.Time
}

func (c jwtClock) Now() time.Time { return c.now() }
