package api

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Timeouts on the embedded *http.Server. Documented in DECISIONS.md
// "HTTP router (issue 028)"; keep in sync if those budgets change.
const (
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
)

// Server wraps a chi-backed handler in *http.Server with sane timeouts and
// exposes Run / Shutdown for cmd/server.
//
// Split from cmd/server so api-level tests can construct one against
// httptest. The boot wiring (signals, slog, version) stays in main.
type Server struct {
	hs *http.Server
}

// NewServer binds handler to addr with the documented timeouts. addr
// follows the net.Listen convention (":8080", "127.0.0.1:8080", etc.).
func NewServer(addr string, handler http.Handler) *Server {
	return &Server{
		hs: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		},
	}
}

// Run blocks on ListenAndServe and returns its error, normalizing the
// expected clean-shutdown sentinel (http.ErrServerClosed) to nil so
// callers only see "real" errors.
func (s *Server) Run() error {
	if err := s.hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown drains in-flight requests within the context's deadline.
// Caller is responsible for the timeout — cmd/server uses 10s.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.hs.Shutdown(ctx)
}

// Addr reports the listener address, useful for tests that bind ":0".
func (s *Server) Addr() string {
	return s.hs.Addr
}
