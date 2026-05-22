// Command server is the single-binary cloud process for Iter. At issue 048
// it ships the boot spine only: argument-free entry point, structured logger,
// stdlib HTTP server bound to PORT, SIGTERM/SIGINT graceful shutdown.
//
// The full router (chi), middleware chain, /health body, and dependency
// wiring (pgxpool, redis, WorkOS verifier, Modal client) land in subsequent
// slices — see internal/app.Deps for the extension points and
// ARCHITECTURE.md §9 Step 3/4 for the build order. Do not import chi, pgx,
// or any cloud SDK from this file until those slices land.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
)

// version is injected at link time via:
//
//	-ldflags "-X main.version=$(git describe --tags --dirty)"
//
// Left as "dev" for local `go run ./cmd/server`. The /health body will
// surface this once issue 028 wires the real handler.
var version = "dev"

// defaultPort is the listen port when $PORT is unset. Matches Railway's
// convention so `railway up` works without extra config.
const defaultPort = "8080"

// shutdownTimeout is the budget for in-flight requests to drain after a
// SIGTERM/SIGINT. Beyond this the listener is hard-closed.
const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Boot-time DB pool. We give the construction call its own context
	// with a tight deadline so an unreachable Postgres fails the boot
	// instead of hanging forever; once running, request contexts carry
	// per-request deadlines.
	pool, err := mustNewPool(logger)
	if err != nil {
		logger.Error("startup failed: postgres pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	deps := app.Deps{
		Logger:       logger,
		BuildVersion: version,
		DB:           pool,
		// BatchDB left nil — Modal worker (issue 046) owns its own
		// iter_batch connection. Re-populate here only when an
		// in-process consumer of DATABASE_URL_BATCH lands.
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	// Stub mux until issue 028 wires api.NewRouter(deps). The 503 body is
	// intentional: anything hitting this binary today is misconfigured —
	// no public surface ships from issue 048.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "iter server skeleton — handlers land in issue 028", http.StatusServiceUnavailable)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := run(srv, deps); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// run is split out so it can be unit-tested without exiting the process.
// It blocks until a SIGTERM/SIGINT arrives, then drains within
// shutdownTimeout and returns.
func run(srv *http.Server, deps app.Deps) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		deps.Logger.Info("server listening",
			"addr", srv.Addr,
			"version", deps.BuildVersion,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		deps.Logger.Info("shutdown signal received, draining",
			"timeout", shutdownTimeout.String(),
		)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// Drain the ListenAndServe goroutine so callers don't leak it.
	if err := <-errCh; err != nil {
		return err
	}
	deps.Logger.Info("server stopped cleanly")
	return nil
}

// dbStartupTimeout caps how long boot waits for Postgres to answer the
// initial ping. Beyond this we exit non-zero and let Railway restart
// (Postgres is in the same project network, so 10s is generous).
const dbStartupTimeout = 10 * time.Second

// mustNewPool reads $DATABASE_URL and builds the request-path pgx pool.
// Returns (nil, error) on any failure — caller exits the process. We
// deliberately do NOT swallow a missing DATABASE_URL: the binary has no
// useful behavior without Postgres, and a silent fallback would mask
// configuration mistakes in CI / Railway.
func mustNewPool(logger *slog.Logger) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbStartupTimeout)
	defer cancel()
	return db.NewPool(ctx, db.PoolConfig{
		DSN:    dsn,
		Logger: logger,
	})
}
