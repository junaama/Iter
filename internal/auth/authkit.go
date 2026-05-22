// Package auth provides WorkOS AuthKit handlers for the redirect-based login flow.
//
// These handlers complement the JWT verifier (verifier.go) by providing the
// login/callback/logout endpoints that obtain the JWTs in the first place.
// The flow:
//
//  1. GET /auth/login → redirect user to WorkOS-hosted login page
//  2. WorkOS redirects back to GET /auth/callback?code=... after auth
//  3. Callback exchanges code for access token + user info
//  4. GET /auth/logout → clears session, redirects home
//
// Session management uses HTTP-only cookies. The sealed session pattern
// from the WorkOS SDK is used for security.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	workos "github.com/workos/workos-go/v8"
)

// AuthKitConfig holds the WorkOS configuration for AuthKit.
type AuthKitConfig struct {
	// APIKey is the WorkOS API key (sk_...).
	APIKey string
	// ClientID is the WorkOS client ID (client_...).
	ClientID string
	// RedirectURI is the callback URL registered in WorkOS dashboard.
	RedirectURI string
	// CookiePassword is the 32-byte secret for sealing session cookies.
	// If empty, a development-only default is used.
	CookiePassword string
	// Logger is optional; if nil, a default logger is used.
	Logger *slog.Logger
}

// AuthKitConfigFromEnv creates an AuthKitConfig from environment variables.
func AuthKitConfigFromEnv() AuthKitConfig {
	return AuthKitConfig{
		APIKey:         os.Getenv("WORKOS_API_KEY"),
		ClientID:       os.Getenv("WORKOS_CLIENT_ID"),
		RedirectURI:    os.Getenv("WORKOS_REDIRECT_URI"),
		CookiePassword: os.Getenv("WORKOS_COOKIE_PASSWORD"),
	}
}

// Validate returns an error if required fields are missing.
func (c AuthKitConfig) Validate() error {
	if c.APIKey == "" {
		return errors.New("auth: WORKOS_API_KEY is required")
	}
	if c.ClientID == "" {
		return errors.New("auth: WORKOS_CLIENT_ID is required")
	}
	if c.RedirectURI == "" {
		return errors.New("auth: WORKOS_REDIRECT_URI is required")
	}
	return nil
}

// AuthKit provides HTTP handlers for WorkOS AuthKit authentication.
type AuthKit struct {
	client       *workos.Client
	publicClient *workos.PublicClient
	cfg          AuthKitConfig
	logger       *slog.Logger
}

// NewAuthKit constructs an AuthKit handler set. Returns an error if
// configuration is invalid.
func NewAuthKit(cfg AuthKitConfig) (*AuthKit, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Standard client for server-side operations (code exchange)
	client := workos.NewClient(
		cfg.APIKey,
		workos.WithClientID(cfg.ClientID),
	)

	// Public client for generating authorization URLs (no API key needed)
	publicClient := workos.NewPublicClient(cfg.ClientID)

	return &AuthKit{
		client:       client,
		publicClient: publicClient,
		cfg:          cfg,
		logger:       logger,
	}, nil
}

// cookiePassword returns the password for sealing session cookies.
// Uses the configured password or a development default.
func (ak *AuthKit) cookiePassword() string {
	if ak.cfg.CookiePassword != "" {
		return ak.cfg.CookiePassword
	}
	// Development-only fallback. In production, set WORKOS_COOKIE_PASSWORD.
	return "iter-dev-cookie-password-32b!"
}

// sessionCookieName is the name of the session cookie.
const sessionCookieName = "iter_session"

// LoginHandler returns an HTTP handler for GET /auth/login.
// It redirects the user to the WorkOS-hosted login page.
func (ak *AuthKit) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Generate authorization URL using PKCE
		result, err := ak.publicClient.GetAuthorizationURL(workos.AuthKitAuthorizationURLParams{
			RedirectURI: ak.cfg.RedirectURI,
		})
		if err != nil {
			ak.logger.Error("failed to get authorization URL", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Store the PKCE code verifier in a temporary cookie for the callback.
		// This cookie is consumed once during the callback and then deleted.
		http.SetCookie(w, &http.Cookie{
			Name:     "iter_pkce_verifier",
			Value:    result.CodeVerifier,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   600, // 10 minutes, plenty for the auth flow
		})

		http.Redirect(w, r, result.URL, http.StatusFound)
	}
}

// CallbackHandler returns an HTTP handler for GET /auth/callback.
// It exchanges the authorization code for tokens and creates a session.
func (ak *AuthKit) CallbackHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			// Check for error response from WorkOS
			if errMsg := r.URL.Query().Get("error"); errMsg != "" {
				errDesc := r.URL.Query().Get("error_description")
				ak.logger.Warn("auth callback error from WorkOS",
					"error", errMsg,
					"description", errDesc,
				)
				http.Error(w, fmt.Sprintf("Authentication failed: %s", errDesc), http.StatusBadRequest)
				return
			}
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			return
		}

		// Retrieve the PKCE code verifier from the cookie
		verifierCookie, err := r.Cookie("iter_pkce_verifier")
		if err != nil {
			ak.logger.Warn("missing PKCE verifier cookie")
			http.Error(w, "Session expired. Please try logging in again.", http.StatusBadRequest)
			return
		}
		codeVerifier := verifierCookie.Value

		// Clear the PKCE verifier cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "iter_pkce_verifier",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1, // Delete the cookie
		})

		// Exchange the authorization code for an authenticated session
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		authResult, err := ak.client.UserManagement().AuthenticateWithCode(ctx, &workos.UserManagementAuthenticateWithCodeParams{
			Code:         code,
			CodeVerifier: &codeVerifier,
		})
		if err != nil {
			ak.logger.Error("failed to authenticate with code", "err", err)
			http.Error(w, "Authentication failed", http.StatusInternalServerError)
			return
		}

		// Create a sealed session cookie using the helper function
		sealedSession, err := workos.SealSessionFromAuthResponse(
			authResult.AccessToken,
			authResult.RefreshToken,
			authResult.User,
			authResult.Impersonator,
			ak.cookiePassword(),
		)
		if err != nil {
			ak.logger.Error("failed to seal session", "err", err)
			http.Error(w, "Session creation failed", http.StatusInternalServerError)
			return
		}

		// Set the session cookie
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    sealedSession,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 7, // 7 days
		})

		ak.logger.Info("user authenticated",
			"user_id", authResult.User.ID,
			"email", authResult.User.Email,
		)

		// Redirect to the home page or dashboard
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// LogoutHandler returns an HTTP handler for GET /auth/logout.
// It clears the session and redirects to the home page.
func (ak *AuthKit) LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Clear the session cookie
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1, // Delete the cookie
		})

		ak.logger.Info("user logged out")

		// Redirect to home page
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// GetSessionUser extracts the authenticated user from the request session.
// Returns nil if no valid session exists.
func (ak *AuthKit) GetSessionUser(r *http.Request) (*workos.User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, err
	}

	session := workos.NewSession(ak.client, cookie.Value, ak.cookiePassword())
	result, err := session.Authenticate()
	if err != nil {
		return nil, err
	}

	if !result.Authenticated {
		return nil, errors.New("session not authenticated")
	}

	return result.User, nil
}

// RegisterRoutes registers the AuthKit routes on a chi.Router or any
// http.Handler-compatible router. The routes are:
//
//	GET /auth/login    - Redirects to WorkOS login
//	GET /auth/callback - Handles OAuth callback
//	GET /auth/logout   - Clears session
func (ak *AuthKit) RegisterRoutes(mux interface {
	Get(pattern string, handler http.HandlerFunc)
}) {
	mux.Get("/auth/login", ak.LoginHandler())
	mux.Get("/auth/callback", ak.CallbackHandler())
	mux.Get("/auth/logout", ak.LogoutHandler())
}
