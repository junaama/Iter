package middleware

import (
	"context"
	"crypto/rand"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
)

// HeaderRequestID is the canonical request-id header. Inbound values are
// honored when well-formed; otherwise the middleware mints a fresh ULID.
const HeaderRequestID = "X-Request-ID"

// maxRequestIDLen caps inbound X-Request-ID length so logs and headers
// stay bounded. 64 chars accommodates ULIDs (26), UUIDs (36), and most
// proxy-generated trace ids.
const maxRequestIDLen = 64

// requestIDCtxKey is unexported so external packages cannot collide.
type requestIDCtxKey struct{}

// WithRequestID returns a new context carrying id. Exported for tests and
// for downstream middleware that synthesizes request ids out-of-band.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

// RequestIDFromContext retrieves the id installed by RequestID. Returns
// ("", false) if the middleware did not run for this request.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDCtxKey{}).(string)
	return id, ok
}

// RequestID is the outermost middleware in the stack. It:
//
//   - Honors a well-formed inbound X-Request-ID (length ≤ 64, ASCII
//     printable, no whitespace) for client-driven correlation.
//   - Otherwise mints a fresh ULID.
//   - Stashes the id on the request context so logger / recover can use it.
//   - Echoes the id back in the X-Request-ID response header *before* the
//     handler writes so even a panic-aborted response carries the header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !isValidRequestID(id) {
			id = newULID()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isValidRequestID accepts ASCII printable strings (no whitespace) with
// length in [1, maxRequestIDLen]. Conservative on purpose — the inbound
// header is untrusted and ends up in our logs.
func isValidRequestID(s string) bool {
	if s == "" || len(s) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		// ASCII printable excludes space (0x20) and DEL (0x7F).
		if c <= 0x20 || c >= 0x7F {
			return false
		}
	}
	return true
}

// newULID mints a fresh monotonic ULID. crypto/rand keeps ids
// unpredictable; ulid.Now provides millisecond ordering for log scans.
func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
