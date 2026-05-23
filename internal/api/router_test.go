package api_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/api"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/auth"
)

func TestRouter_UnregisteredRouteReturns503(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/no-such-route")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// With nil deps.Auth, the auth middleware (031) fires before
	// NotFound and returns 503 auth_unavailable. The original
	// "skeleton" marker only surfaces when auth is wired AND the
	// route is unregistered, which won't be a typical state — once
	// 031's auth is in the chain, every unrouted request to the
	// authed Group either gets auth_unavailable (nil verifier) or
	// invalid_token (real verifier rejecting the missing bearer).
	// Either way, the router IS reachable and IS returning 503;
	// that's what this test asserts.
	if !strings.Contains(string(body), "auth_unavailable") && !strings.Contains(string(body), "skeleton") {
		t.Fatalf("body missing expected marker: %q", string(body))
	}
}

// TestRouter_HealthRegistered verifies that GET /health is wired into
// the chi router (issue 030). The handler runs against a nil-deps shape
// — no DB, no Redis, no LLM/Embed — so we expect 503 because the
// db+redis probes both report "down", but the endpoint must still be
// reachable (i.e. not 404). Detailed shape assertions live in
// internal/api/handler/health_test.go.
func TestRouter_HealthRegistered(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 (nil deps), got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: want application/json got %q", got)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["db"] != "down" || body["redis"] != "down" {
		t.Fatalf("nil deps: want db=down redis=down got %v", body)
	}
}

func TestRouter_DashboardMeRegistered(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	found := false
	if err := chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && route == "/v1/dashboard/me" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if !found {
		t.Fatalf("GET /v1/dashboard/me route not registered")
	}
}

func TestRouter_DashboardTeamRegistered(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	found := false
	if err := chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && route == "/v1/dashboard/team" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if !found {
		t.Fatalf("GET /v1/dashboard/team route not registered")
	}
}

func TestRouter_AccountLifecycleRoutesRegistered(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	want := map[string]bool{
		http.MethodPost + " /v1/account/export":     false,
		http.MethodGet + " /v1/account/export/{id}": false,
		http.MethodPost + " /v1/account/delete":     false,
	}
	if err := chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if _, ok := want[key]; ok {
			want[key] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	for route, found := range want {
		if !found {
			t.Fatalf("route not registered: %s", route)
		}
	}
}

func TestRouter_AccountExportRequiresIdempotencyKey(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	verifier, token := testVerifierAndToken(t, tenantID, userID)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
		Auth:         verifier,
		Redis:        rdb,
	}

	r := api.NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/account/export", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/account/export: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 400 from idempotency middleware, got %d body=%q", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "missing_idempotency_key") {
		t.Fatalf("body missing idempotency marker: %q", string(body))
	}
}

func TestRouter_AccountDeleteRequiresAuth(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	verifier, _ := testVerifierAndToken(t, tenantID, userID)
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
		Auth:         verifier,
	}

	r := api.NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/account/delete", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /v1/account/delete: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 401 invalid_token with missing bearer, got %d body=%q", resp.StatusCode, string(body))
	}
}

func TestRouter_SuggestRegisteredBehindIdempotency(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	verifier, token := testVerifierAndToken(t, tenantID, userID)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
		Auth:         verifier,
		Redis:        rdb,
	}

	r := api.NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/suggest", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/suggest: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 400 from idempotency middleware, got %d body=%q", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "missing_idempotency_key") {
		t.Fatalf("body missing idempotency marker: %q", string(body))
	}
}

func TestServer_TimeoutsAndAddr(t *testing.T) {
	srv := api.NewServer(":0", http.NewServeMux())
	if srv.Addr() != ":0" {
		t.Fatalf("Addr round-trip: got %q want :0", srv.Addr())
	}
}

func testVerifierAndToken(t *testing.T, tenantID, userID uuid.UUID) (*auth.Verifier, string) {
	t.Helper()
	const (
		issuer   = "https://issuer.example.test"
		audience = "iter-test"
		kid      = "router-test-key"
	)
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pub, err := jwk.FromRaw(priv.Public())
	if err != nil {
		t.Fatalf("public jwk: %v", err)
	}
	if err := pub.Set(jwk.KeyIDKey, kid); err != nil {
		t.Fatalf("set public kid: %v", err)
	}
	if err := pub.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		t.Fatalf("set public alg: %v", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		t.Fatalf("add public key: %v", err)
	}

	verifier, err := auth.NewVerifier(auth.VerifierConfig{
		JWKSURL:  "https://issuer.example.test/jwks.json",
		Issuer:   issuer,
		Audience: audience,
		Now:      func() time.Time { return now },
		Fetch: func(context.Context, string) (jwk.Set, error) {
			return set, nil
		},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	tok, err := jwt.NewBuilder().
		Issuer(issuer).
		Audience([]string{audience}).
		Subject(userID.String()).
		Claim("tenant_id", tenantID.String()).
		Claim("token_type", "cli").
		IssuedAt(now.Add(-time.Minute)).
		NotBefore(now.Add(-time.Minute)).
		Expiration(now.Add(time.Hour)).
		JwtID("jti-" + uuid.NewString()).
		Build()
	if err != nil {
		t.Fatalf("token build: %v", err)
	}
	signKey, err := jwk.FromRaw(priv)
	if err != nil {
		t.Fatalf("private jwk: %v", err)
	}
	if err := signKey.Set(jwk.KeyIDKey, kid); err != nil {
		t.Fatalf("set private kid: %v", err)
	}
	if err := signKey.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		t.Fatalf("set private alg: %v", err)
	}
	raw, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, signKey))
	if err != nil {
		t.Fatalf("jwt.Sign: %v", err)
	}
	return verifier, string(raw)
}
