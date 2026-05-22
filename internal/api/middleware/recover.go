package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// genericPanicBody is the response body sent when a handler panics. It
// intentionally leaks no information — the stack trace lands in the log,
// keyed by request_id so on-call can correlate.
const genericPanicBody = `{"error":"internal"}`

// Recover is the innermost middleware in the stack (apart from the
// handler itself). It defers a recover() around next.ServeHTTP, and on
// panic:
//
//   - logs the panic value + full stack at error level (with request_id
//     if available),
//   - writes a generic JSON 500 to the client, preserving the
//     X-Request-ID header that RequestID set on the way in.
//
// http.ErrAbortHandler is treated specially per net/http convention:
// silently re-panicked so the http.Server can suppress its own logging.
func Recover(log *slog.Logger) Mw {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rv := recover()
				if rv == nil {
					return
				}
				if rv == http.ErrAbortHandler {
					// Sentinel — the standard library suppresses its
					// own log for this, and so do we.
					panic(rv)
				}

				attrs := []slog.Attr{
					slog.Any("panic", rv),
					slog.String("stack", string(debug.Stack())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				}
				if id, ok := RequestIDFromContext(r.Context()); ok {
					attrs = append(attrs, slog.String("request_id", id))
				}
				log.LogAttrs(r.Context(), slog.LevelError, "http_panic", attrs...)

				// Best-effort: if the handler already wrote headers
				// the client has seen them and this is a no-op call
				// that the stdlib logs once. Either way we still want
				// the panic recorded.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(genericPanicBody))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
