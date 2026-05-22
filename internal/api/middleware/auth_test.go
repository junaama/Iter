package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/auth"
	"github.com/iter-dev/iter/pkg/contracts"
)

// stubVerifier is a hand-rolled tokenVerifier for tests. It records the
// number of calls (so we can assert "verifier was not invoked" for the
// whitelist tests) and returns a canned (Principal, error) pair.
type stubVerifier struct {
	calls     atomic.Int32
	principal contracts.Principal
	err       error

	// gotToken captures the raw token last passed to Verify; tests
	// that exercise the bearer-extraction path assert against this.
	gotToken atomic.Value // string
}

func (s *stubVerifier) Verify(_ context.Context, raw string) (contracts.Principal, error) {
	s.calls.Add(1)
	s.gotToken.Store(raw)
	return s.principal, s.err
}

func newStubLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// newRequest builds an *http.Request with optional Authorization header.
func newRequest(method, path, authHeader string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	return r
}

// captureHandler returns an http.Handler that records whether it was
// called and what Principal (if any) it observed on the context.
type captureHandler struct {
	called atomic.Bool
	gotP   contracts.Principal
	hasP   bool
}

func (c *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.called.Store(true)
	c.gotP, c.hasP = contracts.PrincipalFromContext(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func TestAuth_ValidToken_PassesPrincipalToNext(t *testing.T) {
	t.Parallel()

	tenant := uuid.New()
	user := uuid.New()
	stub := &stubVerifier{principal: contracts.Principal{UserID: user, TenantID: tenant, TokenID: "jti-1"}}
	log, _ := newStubLogger()
	next := &captureHandler{}

	mw := authMiddleware(stub, WithLogger(log))
	mw(next).ServeHTTP(httptest.NewRecorder(), newRequest(http.MethodGet, "/v1/anything", "Bearer good-token"))

	if !next.called.Load() {
		t.Fatal("next handler was not invoked")
	}
	if !next.hasP {
		t.Fatal("Principal was not attached to request context")
	}
	if next.gotP.UserID != user || next.gotP.TenantID != tenant {
		t.Errorf("Principal mismatch: got %+v", next.gotP)
	}
	if stub.calls.Load() != 1 {
		t.Errorf("verifier should have been called exactly once, got %d", stub.calls.Load())
	}
	if got, _ := stub.gotToken.Load().(string); got != "good-token" {
		t.Errorf("bearer token not forwarded verbatim: got %q", got)
	}
}

func TestAuth_MissingAuthorizationHeader_401(t *testing.T) {
	t.Parallel()

	stub := &stubVerifier{}
	log, _ := newStubLogger()
	next := &captureHandler{}
	mw := authMiddleware(stub, WithLogger(log))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/anything", ""))

	assertUnauthorized(t, rec)
	if next.called.Load() {
		t.Error("next handler should not run on 401")
	}
	if stub.calls.Load() != 0 {
		t.Errorf("verifier should not have been called, got %d", stub.calls.Load())
	}
}

func TestAuth_MalformedAuthorizationHeader_401(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
	}{
		{"wrong_scheme_basic", "Basic dXNlcjpwYXNz"},
		{"wrong_scheme_lowercase_bearer", "bearer tok"},
		{"no_space", "Bearer"},
		{"empty_token", "Bearer "},
		{"only_space", " "},
		{"leading_space", " Bearer tok"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubVerifier{}
			log, _ := newStubLogger()
			next := &captureHandler{}
			mw := authMiddleware(stub, WithLogger(log))

			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/anything", tc.header))

			assertUnauthorized(t, rec)
			if next.called.Load() {
				t.Error("next should not be invoked on malformed header")
			}
			if stub.calls.Load() != 0 {
				t.Errorf("verifier should not have been called, got %d", stub.calls.Load())
			}
		})
	}
}

func TestAuth_VerifyErrors_MapToStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
		wantEvent  string
	}{
		{"expired", auth.ErrExpired, http.StatusUnauthorized, "invalid_token", "auth_token_expired"},
		{"not_yet_valid", auth.ErrNotYetValid, http.StatusUnauthorized, "invalid_token", "auth_token_not_yet_valid"},
		{"invalid_claims", auth.ErrInvalidClaims, http.StatusUnauthorized, "invalid_token", "auth_invalid_claims"},
		{"bad_signature", auth.ErrBadSignature, http.StatusUnauthorized, "invalid_token", "auth_bad_signature"},
		{"malformed", auth.ErrMalformed, http.StatusUnauthorized, "invalid_token", "auth_malformed_token"},
		{"missing_tenant", auth.ErrMissingTenant, http.StatusUnauthorized, "invalid_token", "auth_missing_tenant"},
		{"missing_subject", auth.ErrMissingSubject, http.StatusUnauthorized, "invalid_token", "auth_missing_subject"},
		{"jwks_unavailable", auth.ErrAuthUnavailable, http.StatusServiceUnavailable, "auth_unavailable", "auth_jwks_unavailable"},
		// Sentinels are typically wrapped with %w by the verifier; confirm errors.Is still works.
		{"wrapped_expired", fmt.Errorf("wrap: %w", auth.ErrExpired), http.StatusUnauthorized, "invalid_token", "auth_token_expired"},
		// Defensive default: an unknown error must map to 401, not 500.
		{"unknown_error", errors.New("nope"), http.StatusUnauthorized, "invalid_token", "auth_verify_unknown"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubVerifier{err: tc.err}
			log, buf := newStubLogger()
			next := &captureHandler{}
			mw := authMiddleware(stub, WithLogger(log))

			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/anything", "Bearer tok"))

			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d want %d", rec.Code, tc.wantStatus)
			}
			body, _ := io.ReadAll(rec.Body)
			if !strings.Contains(string(body), tc.wantBody) {
				t.Errorf("body: got %q want substring %q", string(body), tc.wantBody)
			}
			if tc.wantStatus == http.StatusUnauthorized {
				if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="iter"` {
					t.Errorf("WWW-Authenticate: got %q", got)
				}
			}
			if next.called.Load() {
				t.Error("next should not run on verify failure")
			}
			if !strings.Contains(buf.String(), tc.wantEvent) {
				t.Errorf("log does not contain event %q: %s", tc.wantEvent, buf.String())
			}
		})
	}
}

func TestAuth_NilVerifier_ServiceUnavailable(t *testing.T) {
	t.Parallel()

	log, buf := newStubLogger()
	next := &captureHandler{}
	mw := Auth(nil, WithLogger(log))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/anything", "Bearer tok"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d want 503", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "auth_unavailable") {
		t.Errorf("body: got %q", string(body))
	}
	if next.called.Load() {
		t.Error("next should not run when verifier is nil")
	}
	if !strings.Contains(buf.String(), "auth_verifier_unavailable") {
		t.Errorf("expected security event not logged: %s", buf.String())
	}
}

func TestAuth_NilVerifier_WhitelistStillPasses(t *testing.T) {
	t.Parallel()

	log, _ := newStubLogger()
	next := &captureHandler{}
	// A nil verifier must still pass /health through — otherwise the
	// liveness probe would flap when WORKOS_* env vars are unset on
	// initial provisioning.
	mw := Auth(nil, WithSkip("/health"), WithLogger(log))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/health", ""))

	if !next.called.Load() {
		t.Fatal("next should have run for /health")
	}
	if next.hasP {
		t.Error("Principal should not be attached for whitelisted requests")
	}
}

func TestAuth_Whitelist_HealthBypasses(t *testing.T) {
	t.Parallel()

	stub := &stubVerifier{}
	log, _ := newStubLogger()
	next := &captureHandler{}
	mw := authMiddleware(stub, WithSkip("/health", "/v1/webhooks"), WithLogger(log))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/health", ""))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rec.Code)
	}
	if !next.called.Load() {
		t.Error("next should have run for /health")
	}
	if stub.calls.Load() != 0 {
		t.Errorf("verifier should not have been called for whitelisted path, got %d calls", stub.calls.Load())
	}
}

func TestAuth_Whitelist_WebhookBypasses(t *testing.T) {
	t.Parallel()

	stub := &stubVerifier{err: auth.ErrBadSignature} // verifier would 401 if called
	log, _ := newStubLogger()
	next := &captureHandler{}
	mw := authMiddleware(stub, WithSkip("/v1/webhooks"), WithLogger(log))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodPost, "/v1/webhooks/github", ""))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (webhook bypassed auth)", rec.Code)
	}
	if !next.called.Load() {
		t.Error("next should have run for /v1/webhooks/github")
	}
	if stub.calls.Load() != 0 {
		t.Errorf("verifier should not have been called, got %d calls", stub.calls.Load())
	}
}

func TestAuth_Whitelist_NonMatchingPathStillRequiresAuth(t *testing.T) {
	t.Parallel()

	// Make sure a path that LOOKS like a whitelisted prefix but is not
	// (e.g. /v1/webhooksomething) does NOT bypass. Prefix match is
	// exact (no fuzzy matching), so /v1/webhooks/x matches but
	// /v1/webhooksOTHER also matches the prefix /v1/webhooks. Document
	// that risk explicitly: callers must include the trailing slash if
	// they want strict path-segment matching.
	stub := &stubVerifier{}
	log, _ := newStubLogger()
	next := &captureHandler{}
	mw := authMiddleware(stub, WithSkip("/v1/webhooks/"), WithLogger(log))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/webhooksOTHER", ""))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("non-prefix match should require auth: got status %d", rec.Code)
	}
}

func TestWithSkip_EmptyPrefixIgnored(t *testing.T) {
	t.Parallel()

	// An empty prefix string should never match (otherwise it would
	// match every path). Test the defensive branch in isWhitelisted.
	stub := &stubVerifier{principal: contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()}}
	log, _ := newStubLogger()
	next := &captureHandler{}
	mw := authMiddleware(stub, WithSkip(""), WithLogger(log))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/anything", "Bearer tok"))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rec.Code)
	}
	if stub.calls.Load() != 1 {
		t.Errorf("verifier should still be called when only the empty prefix is registered: got %d calls", stub.calls.Load())
	}
}

func TestWithLogger_NilLoggerIgnored(t *testing.T) {
	t.Parallel()

	// WithLogger(nil) must be a no-op so cmd/server can pass an unset
	// dep without nil-panicking the middleware. We verify by confirming
	// that the default slog.Default() path still works.
	stub := &stubVerifier{err: auth.ErrExpired}
	next := &captureHandler{}
	mw := authMiddleware(stub, WithLogger(nil))

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/anything", "Bearer tok"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", rec.Code)
	}
}

func TestAuth_LogSecurityEvent_IncludesRequestID(t *testing.T) {
	t.Parallel()

	stub := &stubVerifier{err: auth.ErrExpired}
	log, buf := newStubLogger()
	next := &captureHandler{}

	// Compose RequestID → Auth so the security log carries the
	// correlation id, matching how router.go wires the chain.
	chain := Chain(RequestID, authMiddleware(stub, WithLogger(log)))
	wrapped := chain(next)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, newRequest(http.MethodGet, "/v1/anything", "Bearer tok"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
	// Just check that *some* request_id field was logged — the value
	// itself is generated inside RequestID and we don't need to assert
	// the ULID format here.
	if !strings.Contains(buf.String(), `"request_id"`) {
		t.Errorf("expected request_id in log: %s", buf.String())
	}
}

// TestAuth_NilVerifierConcreteType exercises the typed-nil branch of
// the public Auth constructor — a *auth.Verifier whose value is nil
// must be detected and forwarded as an interface nil so the nil-check
// inside authMiddleware fires correctly. Without the explicit branch
// in Auth() this would silently produce a non-nil interface wrapping a
// nil pointer and the middleware would panic at Verify().
func TestAuth_NilVerifierConcreteType(t *testing.T) {
	t.Parallel()

	var v *auth.Verifier // typed nil
	mw := Auth(v, WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if mw == nil {
		t.Fatal("Auth returned nil Mw")
	}
	rec := httptest.NewRecorder()
	mw(&captureHandler{}).ServeHTTP(rec, newRequest(http.MethodGet, "/v1/x", "Bearer tok"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("typed-nil verifier: got status %d want 503", rec.Code)
	}
}

// TestAuth_NonNilRealVerifier ensures the non-nil branch of the public
// Auth() constructor wires through to authMiddleware. We construct a
// real *auth.Verifier with stub config — no JWKS fetch happens because
// the whitelist prevents Verify from being called for /health.
func TestAuth_NonNilRealVerifier(t *testing.T) {
	t.Parallel()

	v, err := auth.NewVerifier(auth.VerifierConfig{
		JWKSURL:  "https://example.invalid/.well-known/jwks.json",
		Issuer:   "https://example.invalid",
		Audience: "test-audience",
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	mw := Auth(v,
		WithSkip("/health"),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	next := &captureHandler{}
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newRequest(http.MethodGet, "/health", ""))
	if !next.called.Load() {
		t.Error("whitelist should still bypass with a real verifier")
	}
}

// TestSentinelEventName_FallbackBranch exercises the unreachable-by-
// construction default arm of sentinelEventName. handleVerifyError only
// dispatches to sentinelEventName for known sentinels, so the fallback
// is dead code under normal call paths; we still cover it to guarantee
// the defensive return is correct if a future refactor reuses the
// helper.
func TestSentinelEventName_FallbackBranch(t *testing.T) {
	t.Parallel()

	got := sentinelEventName(errors.New("not a sentinel"))
	if got != "auth_verify_unknown" {
		t.Errorf("sentinelEventName fallback: got %q want auth_verify_unknown", got)
	}
}

// helper assertions

func assertUnauthorized(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="iter"` {
		t.Errorf("WWW-Authenticate: got %q want %q", got, `Bearer realm="iter"`)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "invalid_token") {
		t.Errorf("body: got %q want substring \"invalid_token\"", string(body))
	}
}
