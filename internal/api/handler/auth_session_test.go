package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/auth"
	"github.com/iter-dev/iter/internal/db/repo"
)

// fakeRawToken is a minimal jwt.Token stand-in that just answers the
// two methods the handler reads. Keeps the test surface free of jwx.
type fakeRawToken struct {
	subject string
	claims  map[string]any
}

func (f fakeRawToken) Subject() string { return f.subject }
func (f fakeRawToken) Get(name string) (any, bool) {
	v, ok := f.claims[name]
	return v, ok
}

// fakeWorkOSVerifier is a workosVerifier double. Configure it per-test
// to return a canned token or a sentinel error.
type fakeWorkOSVerifier struct {
	token rawToken
	err   error
}

func (f *fakeWorkOSVerifier) VerifyRaw(_ context.Context, _ string) (rawToken, error) {
	return f.token, f.err
}

// fakeStore is a userTenantStore double.
type fakeStore struct {
	user   repo.User
	tenant repo.Tenant
	role   string
	err    error

	gotWorkOSSub   string
	gotEmail       string
	gotDisplayName string
}

func (s *fakeStore) ResolveOrProvision(_ context.Context, workosSub, email, display string) (repo.User, repo.Tenant, string, error) {
	s.gotWorkOSSub = workosSub
	s.gotEmail = email
	s.gotDisplayName = display
	if s.err != nil {
		return repo.User{}, repo.Tenant{}, "", s.err
	}
	return s.user, s.tenant, s.role, nil
}

// newAuthSessionTestHandler stitches the doubles together with a discard logger
// and a real *auth.IterSigner so the response carries a verifiable JWT.
func newAuthSessionTestHandler(t *testing.T, verifier workosVerifier, store userTenantStore) (*authSessionHandler, *auth.IterSigner, *auth.IterVerifier) {
	t.Helper()
	signer, err := auth.NewIterSigner("test-secret-32-bytes-not-real-only-tests")
	if err != nil {
		t.Fatalf("NewIterSigner: %v", err)
	}
	iterVerifier, err := auth.NewIterVerifier("test-secret-32-bytes-not-real-only-tests")
	if err != nil {
		t.Fatalf("NewIterVerifier: %v", err)
	}
	h := &authSessionHandler{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		workos:     verifier,
		iterSigner: signer,
		store:      store,
	}
	return h, signer, iterVerifier
}

func postJSON(t *testing.T, h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// TestAuthSession_HappyPath: a valid WorkOS token resolves to an Iter
// (user, tenant) and we return a signed Iter JWT that verifies cleanly.
func TestAuthSession_HappyPath(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	tenantID := uuid.New()
	verifier := &fakeWorkOSVerifier{
		token: fakeRawToken{
			subject: "user_01KSEXAMPLE",
			claims:  map[string]any{"email": "founder@example.com", "name": "Founder"},
		},
	}
	store := &fakeStore{
		user:   repo.User{ID: userID, Email: "founder@example.com", DisplayName: "Founder"},
		tenant: repo.Tenant{ID: tenantID, Name: "Founder", Plan: repo.PlanFree},
		role:   repo.RoleOwner,
	}
	h, _, iterVerifier := newAuthSessionTestHandler(t, verifier, store)

	rec := postJSON(t, h.ServeHTTP, AuthSessionRequest{
		WorkOSAccessToken: strings.Repeat("a", 32),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp AuthSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("token_type: got %q", resp.TokenType)
	}
	if resp.ExpiresIn <= 0 {
		t.Errorf("expires_in must be positive, got %d", resp.ExpiresIn)
	}
	principal, err := iterVerifier.Verify(context.Background(), resp.AccessToken)
	if err != nil {
		t.Fatalf("returned JWT did not verify: %v", err)
	}
	if principal.UserID != userID {
		t.Errorf("UserID: got %s want %s", principal.UserID, userID)
	}
	if principal.TenantID != tenantID {
		t.Errorf("TenantID: got %s want %s", principal.TenantID, tenantID)
	}
	if store.gotWorkOSSub != "user_01KSEXAMPLE" {
		t.Errorf("store saw workos_sub %q", store.gotWorkOSSub)
	}
	if store.gotEmail != "founder@example.com" {
		t.Errorf("store saw email %q", store.gotEmail)
	}
}

// TestAuthSession_503WhenUnconfigured exercises the boot-without-secret
// fail-loud posture. No WorkOS verifier ⇒ 503, NOT 401.
func TestAuthSession_503WhenUnconfigured(t *testing.T) {
	t.Parallel()

	h, _, _ := newAuthSessionTestHandler(t, nil, &fakeStore{})
	rec := postJSON(t, h.ServeHTTP, AuthSessionRequest{WorkOSAccessToken: strings.Repeat("a", 32)})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503 got %d", rec.Code)
	}
}

// TestAuthSession_401OnWorkOSReject collapses every WorkOS verifier
// failure into the generic invalid_token body — clients can't tell
// "expired" from "bad signature" from the response.
func TestAuthSession_401OnWorkOSReject(t *testing.T) {
	t.Parallel()

	verifier := &fakeWorkOSVerifier{err: auth.ErrExpired}
	h, _, _ := newAuthSessionTestHandler(t, verifier, &fakeStore{})

	rec := postJSON(t, h.ServeHTTP, AuthSessionRequest{WorkOSAccessToken: strings.Repeat("a", 32)})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401 got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_token") {
		t.Errorf("body: want invalid_token got %s", rec.Body.String())
	}
}

// TestAuthSession_400OnBadBody covers the parse-failure path: missing
// field, empty body, garbage JSON all collapse to 400.
func TestAuthSession_400OnBadBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"missing_field", `{}`},
		{"wrong_type", `{"workos_access_token": 42}`},
		{"empty_token", `{"workos_access_token": ""}`},
		{"whitespace_token", `{"workos_access_token": "   "}`},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verifier := &fakeWorkOSVerifier{}
			h, _, _ := newAuthSessionTestHandler(t, verifier, &fakeStore{})

			req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: want 400 got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestAuthSession_500OnStoreError surfaces DB failures as 500. We do
// NOT 401 here — the credentials passed verification, the failure is
// internal — and the client should not retry as if the token were bad.
func TestAuthSession_500OnStoreError(t *testing.T) {
	t.Parallel()

	verifier := &fakeWorkOSVerifier{
		token: fakeRawToken{subject: "user_01KSX"},
	}
	store := &fakeStore{err: errors.New("postgres down")}
	h, _, _ := newAuthSessionTestHandler(t, verifier, store)

	rec := postJSON(t, h.ServeHTTP, AuthSessionRequest{WorkOSAccessToken: strings.Repeat("a", 32)})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500 got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAuthSession_FallbackEmail confirms the email derivation when the
// WorkOS token omits an `email` claim — the access-token shape on the
// WorkOS device-code path frequently does.
func TestAuthSession_FallbackEmail(t *testing.T) {
	t.Parallel()

	verifier := &fakeWorkOSVerifier{
		token: fakeRawToken{
			subject: "user_01KSNOFEEMAIL",
			claims:  map[string]any{}, // no email, no name
		},
	}
	store := &fakeStore{
		user:   repo.User{ID: uuid.New()},
		tenant: repo.Tenant{ID: uuid.New()},
		role:   repo.RoleOwner,
	}
	h, _, _ := newAuthSessionTestHandler(t, verifier, store)

	rec := postJSON(t, h.ServeHTTP, AuthSessionRequest{WorkOSAccessToken: strings.Repeat("a", 32)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	// The store should see an empty email hint (handler does not
	// invent one until inside the store). The fallback to
	// "<sub>@dev.iter" happens in liveUserTenantStore, not here.
	if store.gotEmail != "" {
		t.Errorf("store gotEmail: want \"\" got %q", store.gotEmail)
	}
	if store.gotDisplayName != "" {
		t.Errorf("store gotDisplayName: want \"\" got %q", store.gotDisplayName)
	}
}

// TestAuthSession_RejectsNonPOST mirrors the chi router-level
// expectation: only POST is registered, GET reaches the handler only
// in test contexts but must not crash.
func TestAuthSession_RejectsOversizedBody(t *testing.T) {
	t.Parallel()

	verifier := &fakeWorkOSVerifier{}
	h, _, _ := newAuthSessionTestHandler(t, verifier, &fakeStore{})

	huge := strings.Repeat("x", authSessionMaxBodyBytes*2)
	body := `{"workos_access_token": "` + huge + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400 (body cap) got %d", rec.Code)
	}
}
