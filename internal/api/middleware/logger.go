package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/iter-dev/iter/pkg/contracts"
)

// statusRecorder wraps http.ResponseWriter to capture status code and
// bytes written. Handlers that never call WriteHeader implicitly emit
// 200 (per net/http) — we default status to that to match.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		// Implicit 200 — mirror net/http semantics so the recorded
		// status matches what the client actually saw.
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Logger emits exactly one structured log line per request at completion.
// Severity is keyed off the response status code:
//
//   - 5xx → error
//   - 4xx → info
//   - else → debug
//
// The actual visibility of debug lines is controlled by the slog.Handler
// level configured at boot (LOG_LEVEL is honored by cmd/server). This
// middleware does not consult LOG_LEVEL itself — slog already does.
//
// Fields emitted: method, path, status, bytes, duration_ms, request_id,
// and (when a Principal is on the context) tenant_id and user_id.
// Request bodies are never logged: privacy + size + PII.
func Logger(log *slog.Logger) Mw {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			dur := time.Since(start)

			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Int64("duration_ms", dur.Milliseconds()),
			}
			if id, ok := RequestIDFromContext(r.Context()); ok {
				attrs = append(attrs, slog.String("request_id", id))
			}
			if p, ok := contracts.PrincipalFromContext(r.Context()); ok {
				attrs = append(attrs, slog.String("tenant_id", p.TenantID.String()))
				attrs = append(attrs, slog.String("user_id", p.UserID.String()))
			}

			level := levelForStatus(rec.status)
			// LogAttrs avoids the reflection slow path slog.Log uses.
			log.LogAttrs(r.Context(), level, "http_request", attrs...)
		})
	}
}

// levelForStatus maps an HTTP status to a slog level. Pulled out so
// tests can assert the mapping without spinning up a server.
func levelForStatus(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}
