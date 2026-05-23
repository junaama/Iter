package contracts

// Wire shapes for POST /v1/auth/session — the WorkOS access token →
// Iter session JWT exchange endpoint. Mirrors contracts.py
// AuthSessionRequest / AuthSessionResponse. CLAUDE.md invariant:
// changes to either side MUST land in the same commit.
//
// Endpoint: POST /v1/auth/session
// Auth:    no Authorization header required (this is where the Iter
//          session JWT is obtained in the first place).
// Body:    AuthSessionRequest
// Returns: 200 AuthSessionResponse / 400 invalid_request /
//          401 invalid_token / 503 auth_unavailable / 500 internal_error

// AuthSessionRequest is the JSON request body. Send the raw WorkOS
// access token (the device-code flow's `access_token` claim) — NOT
// the refresh token, NOT the id token. The server pins the token's
// expected issuer against $WORKOS_ISSUER.
type AuthSessionRequest struct {
	WorkOSAccessToken string `json:"workos_access_token"`
}

// AuthSessionResponse is the JSON success body. `access_token` is a
// freshly-minted Iter HS256 JWT carrying the Iter user/tenant UUIDs;
// pass it as `Authorization: Bearer <access_token>` to every
// subsequent /v1/* request. `expires_in` is seconds (OAuth2 convention).
type AuthSessionResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"` // always "Bearer"
}
