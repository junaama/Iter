// Package handler — auth_session.go implements POST /v1/auth/session, the
// WorkOS-access-token → Iter-session-JWT exchange endpoint.
//
// Flow:
//
//  1. Mac app (or CLI) signs in via WorkOS device-code, obtains a WorkOS
//     access token.
//  2. Mac app posts the raw WorkOS token here.
//  3. We verify the WorkOS token against WorkOS's JWKS (using the same
//     pinned issuer the request-path verifier uses).
//  4. We pull the WorkOS `sub` ("user_01KS...") and upsert a row in
//     `users` keyed by `workos_user_id`. First-time sign-in mints a
//     personal tenant + tenant_users(role=owner).
//  5. We sign + return an Iter HS256 session JWT carrying the Iter
//     UUIDs the rest of the stack expects.
//
// This endpoint sits OUTSIDE the authenticated middleware group — by
// definition, callers do not yet hold an Iter JWT. Idempotency is also
// skipped (the middleware requires an authenticated Principal; the
// caller can safely retry because the upsert path is keyed on
// workos_user_id and returns the same Iter JWT shape regardless of
// "new" vs "existing"). Per CLAUDE.md the wire shape is mirrored in
// contracts.py + pkg/contracts.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/auth"
	"github.com/iter-dev/iter/internal/db/repo"
)

// authSessionMaxBodyBytes caps the request body. WorkOS access tokens
// are ~3 KB; we allow generous headroom for occasional larger JWTs.
const authSessionMaxBodyBytes = 16 * 1024

// AuthSessionHandler returns the HTTP handler for POST /v1/auth/session.
// The handler is constructed from app.Deps so a future wiring change
// (e.g. swapping the WorkOS verifier for a multi-IdP router) doesn't
// churn the route registration in router.go.
func AuthSessionHandler(deps app.Deps) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var v workosVerifier
	if deps.WorkOSVerifier != nil {
		v = workosVerifierAdapter{v: deps.WorkOSVerifier}
	}
	var store userTenantStore
	if deps.DB != nil {
		store = liveUserTenantStore{pool: deps.DB}
	}
	h := &authSessionHandler{
		logger:     logger,
		workos:     v,
		iterSigner: deps.IterSigner,
		store:      store,
	}
	return h.ServeHTTP
}

// rawToken is the subset of jwt.Token the handler reads. Defined as an
// interface so unit tests can inject a fake without standing up a JWKS
// server or building a real jwt.Token.
type rawToken interface {
	Subject() string
	Get(name string) (any, bool)
}

// workosVerifier is the subset of *auth.Verifier the handler uses.
// Same purpose as rawToken: keep the test surface independent of the
// jwx package details.
type workosVerifier interface {
	VerifyRaw(ctx context.Context, raw string) (rawToken, error)
}

// workosVerifierAdapter wraps *auth.Verifier so its VerifyRaw — which
// returns the concrete jwt.Token — satisfies workosVerifier. The
// adapter is the only place the jwx type appears in this file.
type workosVerifierAdapter struct {
	v *auth.Verifier
}

func (a workosVerifierAdapter) VerifyRaw(ctx context.Context, raw string) (rawToken, error) {
	tok, err := a.v.VerifyRaw(ctx, raw)
	if err != nil {
		return nil, err
	}
	return tok, nil
}

// userTenantStore is the persistence surface the handler needs. The
// production implementation is liveUserTenantStore (bottom of file)
// which runs a single transaction on *pgxpool.Pool; unit tests inject
// a fake to avoid spinning up Postgres.
type userTenantStore interface {
	ResolveOrProvision(ctx context.Context, workosSub, emailHint, displayNameHint string) (repo.User, repo.Tenant, string, error)
}

type authSessionHandler struct {
	logger     *slog.Logger
	workos     workosVerifier
	iterSigner *auth.IterSigner
	store      userTenantStore
}

// AuthSessionRequest is the wire body for POST /v1/auth/session. Mirror
// of contracts.py AuthSessionRequest.
type AuthSessionRequest struct {
	WorkOSAccessToken string `json:"workos_access_token"`
}

// AuthSessionResponse is the wire body returned on success. Mirror of
// contracts.py AuthSessionResponse.
type AuthSessionResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"` // seconds; matches OAuth2 convention
	TokenType   string `json:"token_type"` // always "Bearer"
}

// authSessionErrBody is the JSON shape for error responses. Mirrors the
// invalidTokenBody / authUnavailableBody constants the middleware uses
// so clients see a consistent shape regardless of which layer rejected
// them.
type authSessionErrBody struct {
	Error string `json:"error"`
}

// ServeHTTP runs the exchange.
//
// 503: WorkOS verifier or Iter signer not configured (deploy is
//
//	under-configured; do NOT 401 because the failure is not a credential
//	problem and a client retry won't help until the env is fixed).
//
// 400: request body unparseable.
// 401: WorkOS token rejected (expired / bad signature / etc.).
// 500: database / minting failure.
// 200: success — body is AuthSessionResponse.
func (h *authSessionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.workos == nil || h.iterSigner == nil {
		h.logger.LogAttrs(r.Context(), slog.LevelError,
			"auth_session_unavailable",
			slog.Bool("have_workos", h.workos != nil),
			slog.Bool("have_signer", h.iterSigner != nil),
		)
		writeAuthSessionError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	if h.store == nil {
		h.logger.LogAttrs(r.Context(), slog.LevelError, "auth_session_store_nil")
		writeAuthSessionError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}

	req, err := parseAuthSessionRequest(r)
	if err != nil {
		writeAuthSessionError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	tok, err := h.workos.VerifyRaw(r.Context(), req.WorkOSAccessToken)
	if err != nil {
		// Do not echo the underlying sentinel back to the client:
		// every verifier failure collapses to "your token is bad" to
		// match the auth middleware's posture (defense against
		// probing). Detail goes to structured logs only.
		h.logSecurityEvent(r, "auth_session_workos_verify_failed", err)
		writeAuthSessionError(w, http.StatusUnauthorized, "invalid_token")
		return
	}

	workosSub := strings.TrimSpace(tok.Subject())
	if workosSub == "" {
		h.logSecurityEvent(r, "auth_session_workos_missing_sub", nil)
		writeAuthSessionError(w, http.StatusUnauthorized, "invalid_token")
		return
	}

	email := extractStringClaim(tok, "email")
	displayName := extractStringClaim(tok, "name")

	user, tenant, role, err := h.store.ResolveOrProvision(r.Context(), workosSub, email, displayName)
	if err != nil {
		h.logger.LogAttrs(r.Context(), slog.LevelError,
			"auth_session_provision_failed",
			slog.String("err", err.Error()),
		)
		writeAuthSessionError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	claims := auth.IterTokenClaims{
		UserID:   user.ID,
		TenantID: tenant.ID,
	}
	if role != "" {
		claims.Roles = []string{role}
	}

	signed, err := h.iterSigner.Sign(claims)
	if err != nil {
		h.logger.LogAttrs(r.Context(), slog.LevelError,
			"auth_session_sign_failed",
			slog.String("err", err.Error()),
		)
		writeAuthSessionError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	h.logger.LogAttrs(r.Context(), slog.LevelInfo,
		"auth_session_issued",
		slog.String("iter_user_id", user.ID.String()),
		slog.String("iter_tenant_id", tenant.ID.String()),
		// workos_user_id is NOT a credential — it's the prefixed
		// public id like user_01KS... — so logging it is acceptable
		// and useful for support.
		slog.String("workos_user_id", workosSub),
	)

	resp := AuthSessionResponse{
		AccessToken: signed,
		ExpiresIn:   int64(h.iterSigner.TTL().Seconds()),
		TokenType:   "Bearer",
	}
	writeAuthSessionJSON(w, http.StatusOK, resp)
}

// liveUserTenantStore is the production implementation of
// userTenantStore. It runs the whole resolve-or-provision sequence in
// a single transaction on the request-path pool. `users`, `tenants`,
// `tenant_users` are NOT under RLS (see migrations/0001_initial.sql)
// so this can skip db.WithTenant — there is no app.current_tenant to
// set.
type liveUserTenantStore struct {
	pool *pgxpool.Pool
}

// ResolveOrProvision returns the (user, tenant, role) triple that owns
// workosSub. On first sign-in it mints a personal tenant (plan=free)
// and a tenant_users(role=owner) row. On subsequent sign-ins it
// reuses the existing user and picks the first membership by
// joined_at ASC (multi-tenant membership UI is out of scope for v1).
// The returned `role` is the membership role for the chosen tenant.
func (s liveUserTenantStore) ResolveOrProvision(
	ctx context.Context,
	workosSub, emailHint, displayNameHint string,
) (repo.User, repo.Tenant, string, error) {
	var (
		user   repo.User
		tenant repo.Tenant
		role   string
	)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return user, tenant, "", fmt.Errorf("auth_session.begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	existing, err := repo.GetUserByWorkOSID(ctx, tx, workosSub)
	switch {
	case err == nil:
		user = existing
	case errors.Is(err, pgx.ErrNoRows):
		// First-time sign-in: mint user + personal tenant + owner row.
		email := emailHint
		if email == "" {
			// WorkOS access tokens may omit `email` (the AuthKit
			// session-token shape often does). Derive a deterministic
			// placeholder so the citext UNIQUE constraint on email
			// is satisfied; users can update their email later via
			// account settings (out of scope for v1).
			email = fmt.Sprintf("%s@dev.iter", workosSub)
		}
		display := displayNameHint
		if display == "" {
			display = workosSub
		}
		newUser, ierr := repo.InsertUserWithWorkOS(ctx, tx, email, display, workosSub)
		if ierr != nil {
			return user, tenant, "", fmt.Errorf("auth_session.insert_user: %w", ierr)
		}
		user = newUser

		tenantName := display
		if tenantName == "" {
			tenantName = "Personal"
		}
		newTenant, terr := repo.InsertTenant(ctx, tx, tenantName, repo.PlanFree)
		if terr != nil {
			return user, tenant, "", fmt.Errorf("auth_session.insert_tenant: %w", terr)
		}
		tenant = newTenant

		_, merr := repo.InsertTenantUser(ctx, tx, tenant.ID, user.ID, repo.RoleOwner)
		if merr != nil {
			return user, tenant, "", fmt.Errorf("auth_session.insert_tenant_user: %w", merr)
		}
		role = repo.RoleOwner
	default:
		return user, tenant, "", fmt.Errorf("auth_session.get_user_by_workos: %w", err)
	}

	if tenant.ID == uuid.Nil {
		// Existing user: pick the first tenant by joined_at ASC.
		// Multi-tenant membership is out of scope; v1 just returns
		// the personal tenant. Future: allow the client to request
		// a specific tenant id at exchange time.
		memberships, lerr := repo.ListTenantUsersByUser(ctx, tx, user.ID)
		if lerr != nil {
			return user, tenant, "", fmt.Errorf("auth_session.list_memberships: %w", lerr)
		}
		if len(memberships) == 0 {
			// Defensive: a user without a tenant cannot use the
			// request path because RLS-scoped repos require one.
			// Mint a personal tenant on the fly.
			display := user.DisplayName
			if display == "" {
				display = "Personal"
			}
			newTenant, terr := repo.InsertTenant(ctx, tx, display, repo.PlanFree)
			if terr != nil {
				return user, tenant, "", fmt.Errorf("auth_session.repair_tenant: %w", terr)
			}
			tenant = newTenant
			if _, merr := repo.InsertTenantUser(ctx, tx, tenant.ID, user.ID, repo.RoleOwner); merr != nil {
				return user, tenant, "", fmt.Errorf("auth_session.repair_membership: %w", merr)
			}
			role = repo.RoleOwner
		} else {
			membership := memberships[0]
			existingTenant, gerr := repo.GetTenant(ctx, tx, membership.TenantID)
			if gerr != nil {
				return user, tenant, "", fmt.Errorf("auth_session.get_tenant: %w", gerr)
			}
			tenant = existingTenant
			role = membership.Role
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return user, tenant, "", fmt.Errorf("auth_session.commit: %w", err)
	}
	committed = true
	return user, tenant, role, nil
}

// logSecurityEvent emits a Warn-level log with a stable event name. We
// deliberately do NOT log the raw WorkOS token — even a rejected token
// is bearer credentials.
func (h *authSessionHandler) logSecurityEvent(r *http.Request, event string, err error) {
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	h.logger.LogAttrs(r.Context(), slog.LevelWarn, event, attrs...)
}

// parseAuthSessionRequest reads the JSON body, capping at
// authSessionMaxBodyBytes. Empty / unparseable / missing-token bodies
// return a sentinel error so the caller emits a 400.
func parseAuthSessionRequest(r *http.Request) (AuthSessionRequest, error) {
	var req AuthSessionRequest
	limited := http.MaxBytesReader(nil, r.Body, authSessionMaxBodyBytes)
	defer limited.Close()
	body, err := io.ReadAll(limited)
	if err != nil {
		return req, fmt.Errorf("read body: %w", err)
	}
	if len(body) == 0 {
		return req, errors.New("empty body")
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, fmt.Errorf("unmarshal: %w", err)
	}
	if strings.TrimSpace(req.WorkOSAccessToken) == "" {
		return req, errors.New("workos_access_token required")
	}
	return req, nil
}

// extractStringClaim pulls an optional string claim. Missing or
// non-string values return "".
func extractStringClaim(tok rawToken, name string) string {
	v, ok := tok.Get(name)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// writeAuthSessionJSON writes a JSON response with no caching. Cache-
// Control: no-store matches the rest of the API.
func writeAuthSessionJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeAuthSessionError emits the canonical error envelope.
func writeAuthSessionError(w http.ResponseWriter, status int, code string) {
	writeAuthSessionJSON(w, status, authSessionErrBody{Error: code})
}

// Note: the wire shapes for this endpoint are also mirrored in
// pkg/contracts/auth_session.go and contracts.py; changes here MUST
// update both per CLAUDE.md.
