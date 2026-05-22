package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/iter-dev/iter/internal/auth"
)

const (
	testIssuer   = "https://api.workos.com"
	testAudience = "iter-dev"
	testKID      = "test-key-1"
)

// testKeys is the keypair + JWKS used by every test in this file. Generated
// once in newTestKeys() to keep tests fast (RSA gen is the slow part).
type testKeys struct {
	priv   *rsa.PrivateKey
	pubSet jwk.Set
}

func newTestKeys(t *testing.T) *testKeys {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pub, err := jwk.FromRaw(priv.Public())
	if err != nil {
		t.Fatalf("jwk.FromRaw: %v", err)
	}
	if err := pub.Set(jwk.KeyIDKey, testKID); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	if err := pub.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		t.Fatalf("set alg: %v", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		t.Fatalf("add key: %v", err)
	}
	return &testKeys{priv: priv, pubSet: set}
}

type tokenOpts struct {
	issuer   string
	audience string
	subject  string
	tenantID string
	iat      time.Time
	exp      time.Time
	nbf      time.Time
	kid      string
	noTenant bool
	roles    []string
	jti      string
}

func defaultTokenOptsAt(now time.Time) tokenOpts {
	return tokenOpts{
		issuer:   testIssuer,
		audience: testAudience,
		subject:  uuid.NewString(),
		tenantID: uuid.NewString(),
		iat:      now.Add(-1 * time.Minute),
		exp:      now.Add(1 * time.Hour),
		nbf:      now.Add(-1 * time.Minute),
		kid:      testKID,
		jti:      "jti-" + uuid.NewString(),
	}
}

func defaultTokenOpts() tokenOpts { return defaultTokenOptsAt(time.Now()) }

func (tk *testKeys) sign(t *testing.T, o tokenOpts) string {
	t.Helper()
	iat := o.iat
	if iat.IsZero() {
		iat = time.Now()
	}
	b := jwt.NewBuilder().
		Issuer(o.issuer).
		Audience([]string{o.audience}).
		Subject(o.subject).
		Expiration(o.exp).
		NotBefore(o.nbf).
		IssuedAt(iat).
		JwtID(o.jti)
	if !o.noTenant {
		b = b.Claim("tenant_id", o.tenantID)
	}
	if len(o.roles) > 0 {
		b = b.Claim("roles", o.roles)
	}
	tok, err := b.Build()
	if err != nil {
		t.Fatalf("token build: %v", err)
	}

	signKey, err := jwk.FromRaw(tk.priv)
	if err != nil {
		t.Fatalf("priv jwk: %v", err)
	}
	if err := signKey.Set(jwk.KeyIDKey, o.kid); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	if err := signKey.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		t.Fatalf("set alg: %v", err)
	}
	raw, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, signKey))
	if err != nil {
		t.Fatalf("jwt.Sign: %v", err)
	}
	return string(raw)
}

// fakeFetcher returns the pre-built JWKS unless `fail` is set, in which case
// it errors. Counts calls so tests can assert cache behavior.
type fakeFetcher struct {
	set    jwk.Set
	calls  int32
	fail   atomic.Bool
	errVal error
}

func (f *fakeFetcher) fetch(_ context.Context, _ string) (jwk.Set, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.fail.Load() {
		if f.errVal != nil {
			return nil, f.errVal
		}
		return nil, errors.New("network down")
	}
	return f.set, nil
}

func (f *fakeFetcher) callCount() int32 { return atomic.LoadInt32(&f.calls) }

// fixedClock returns a controllable time.Now.
type fixedClock struct {
	t atomic.Value // time.Time
}

func newClock(start time.Time) *fixedClock {
	c := &fixedClock{}
	c.t.Store(start)
	return c
}
func (c *fixedClock) now() time.Time          { return c.t.Load().(time.Time) }
func (c *fixedClock) advance(d time.Duration) { c.t.Store(c.now().Add(d)) }
func (c *fixedClock) set(t time.Time)         { c.t.Store(t) }

func newVerifier(t *testing.T, tk *testKeys, f *fakeFetcher, clk *fixedClock) *auth.Verifier {
	t.Helper()
	v, err := auth.NewVerifier(auth.VerifierConfig{
		JWKSURL:     "https://example.test/jwks.json",
		Issuer:      testIssuer,
		Audience:    testAudience,
		FreshTTL:    1 * time.Hour,
		StaleWindow: 10 * time.Minute,
		Leeway:      30 * time.Second,
		Now:         clk.now,
		Fetch:       f.fetch,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestVerify_TableDriven(t *testing.T) {
	tk := newTestKeys(t)
	clkBase := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		tweak    func(o *tokenOpts)
		signWith func(t *testing.T) string // optional override: produce a raw token bypassing tk
		wantErr  error                     // sentinel from auth package, or nil
	}{
		{
			name:    "valid token",
			tweak:   func(o *tokenOpts) {},
			wantErr: nil,
		},
		{
			name: "expired token",
			tweak: func(o *tokenOpts) {
				o.iat = clkBase.Add(-2 * time.Hour)
				o.nbf = clkBase.Add(-2 * time.Hour)
				o.exp = clkBase.Add(-1 * time.Hour)
			},
			wantErr: auth.ErrExpired,
		},
		{
			name:    "wrong issuer",
			tweak:   func(o *tokenOpts) { o.issuer = "https://evil.example.com" },
			wantErr: auth.ErrInvalidClaims,
		},
		{
			name:    "wrong audience",
			tweak:   func(o *tokenOpts) { o.audience = "some-other-app" },
			wantErr: auth.ErrInvalidClaims,
		},
		{
			name:    "missing tenant_id",
			tweak:   func(o *tokenOpts) { o.noTenant = true },
			wantErr: auth.ErrMissingTenant,
		},
		{
			name:    "non-uuid tenant_id",
			tweak:   func(o *tokenOpts) { o.tenantID = "not-a-uuid" },
			wantErr: auth.ErrMissingTenant,
		},
		{
			name:    "non-uuid sub",
			tweak:   func(o *tokenOpts) { o.subject = "definitely-not-uuid" },
			wantErr: auth.ErrMissingSubject,
		},
		{
			name:     "malformed token (not three segments)",
			signWith: func(t *testing.T) string { return "not.a.jwt.really" },
			wantErr:  auth.ErrMalformed,
		},
		{
			name:     "empty token",
			signWith: func(t *testing.T) string { return "" },
			wantErr:  auth.ErrMalformed,
		},
		{
			name: "bad signature",
			signWith: func(t *testing.T) string {
				// Sign with a DIFFERENT key — the verifier's JWKS won't match.
				other := newTestKeys(t)
				return other.sign(t, defaultTokenOptsAt(clkBase))
			},
			wantErr: auth.ErrBadSignature,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clk := newClock(clkBase)
			fetcher := &fakeFetcher{set: tk.pubSet}
			v := newVerifier(t, tk, fetcher, clk)

			var raw string
			if tc.signWith != nil {
				raw = tc.signWith(t)
			} else {
				o := defaultTokenOptsAt(clkBase)
				tc.tweak(&o)
				raw = tk.sign(t, o)
			}

			_, err := v.Verify(context.Background(), raw)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestVerify_PrincipalFields(t *testing.T) {
	tk := newTestKeys(t)
	clk := newClock(time.Now())
	fetcher := &fakeFetcher{set: tk.pubSet}
	v := newVerifier(t, tk, fetcher, clk)

	o := defaultTokenOpts()
	o.roles = []string{"admin", "billing"}
	raw := tk.sign(t, o)

	p, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.UserID.String() != o.subject {
		t.Errorf("UserID: got %s, want %s", p.UserID, o.subject)
	}
	if p.TenantID.String() != o.tenantID {
		t.Errorf("TenantID: got %s, want %s", p.TenantID, o.tenantID)
	}
	if p.TokenID != o.jti {
		t.Errorf("TokenID: got %s, want %s", p.TokenID, o.jti)
	}
	if len(p.Roles) != 2 || p.Roles[0] != "admin" || p.Roles[1] != "billing" {
		t.Errorf("Roles: got %v, want [admin billing]", p.Roles)
	}
}

func TestVerify_JWKSUnreachable_WarmCache(t *testing.T) {
	// Scenario: first call populates cache; JWKS endpoint then dies; calls
	// within fresh+stale window still succeed against the cached keys.
	tk := newTestKeys(t)
	clk := newClock(time.Now())
	fetcher := &fakeFetcher{set: tk.pubSet}
	v := newVerifier(t, tk, fetcher, clk)

	raw := tk.sign(t, defaultTokenOpts())

	// Prime the cache.
	if _, err := v.Verify(context.Background(), raw); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if got := fetcher.callCount(); got != 1 {
		t.Fatalf("expected 1 fetch, got %d", got)
	}

	// Network goes down.
	fetcher.fail.Store(true)

	// Within fresh TTL → no refresh attempted; cache serves the token.
	clk.advance(30 * time.Minute)
	if _, err := v.Verify(context.Background(), raw); err != nil {
		t.Fatalf("within fresh TTL with cache: %v", err)
	}
	if got := fetcher.callCount(); got != 1 {
		t.Errorf("fresh TTL should not refetch; got %d calls", got)
	}
}

func TestVerify_JWKSUnreachable_ColdCache(t *testing.T) {
	// Scenario: very first Verify call hits a dead JWKS endpoint.
	// Expected: ErrAuthUnavailable so callers can return a 503, distinct from
	// the 401 they'd return on a real auth failure.
	tk := newTestKeys(t)
	clk := newClock(time.Now())
	fetcher := &fakeFetcher{set: tk.pubSet}
	fetcher.fail.Store(true)
	v := newVerifier(t, tk, fetcher, clk)

	raw := tk.sign(t, defaultTokenOpts())
	_, err := v.Verify(context.Background(), raw)
	if !errors.Is(err, auth.ErrAuthUnavailable) {
		t.Fatalf("expected ErrAuthUnavailable, got %v", err)
	}
}

func TestVerify_CacheTTL_RefreshAfterExpiry(t *testing.T) {
	// Scenario: cache populated; we cross the fresh+stale window; next call
	// MUST trigger a synchronous fetch.
	start := time.Now()
	tk := newTestKeys(t)
	clk := newClock(start)
	fetcher := &fakeFetcher{set: tk.pubSet}
	v := newVerifier(t, tk, fetcher, clk)

	// Token has to outlive the planned 2h clock advance — give it a 24h exp.
	o := defaultTokenOptsAt(start)
	o.exp = start.Add(24 * time.Hour)
	raw := tk.sign(t, o)

	if _, err := v.Verify(context.Background(), raw); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if got := fetcher.callCount(); got != 1 {
		t.Fatalf("prime fetch: got %d", got)
	}

	// Go past freshTTL + staleWindow (1h + 10m = 1h10m).
	clk.advance(2 * time.Hour)
	if _, err := v.Verify(context.Background(), raw); err != nil {
		t.Fatalf("post-expiry verify: %v", err)
	}
	if got := fetcher.callCount(); got != 2 {
		t.Errorf("expected synchronous refresh after expiry; got %d total fetches", got)
	}
}

func TestVerify_NewVerifier_Validation(t *testing.T) {
	cases := []struct {
		name string
		cfg  auth.VerifierConfig
	}{
		{"missing jwks url", auth.VerifierConfig{Issuer: "x", Audience: "y"}},
		{"missing issuer", auth.VerifierConfig{JWKSURL: "x", Audience: "y"}},
		// Audience is OPTIONAL — WorkOS AuthKit JWTs omit the aud
		// claim. A missing-audience config must succeed; see
		// verifier.go NewVerifier comments.
	}
	t.Run("audience-optional accepts empty", func(t *testing.T) {
		_, err := auth.NewVerifier(auth.VerifierConfig{JWKSURL: "x", Issuer: "y"})
		if err != nil {
			t.Fatalf("audience-empty config should succeed; got: %v", err)
		}
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := auth.NewVerifier(tc.cfg); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// Compile-time guard: jws is used indirectly by the verifier under test, but
// we import it here to make sure the test file's dep set matches production.
var _ = jws.Parse
