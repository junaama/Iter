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

	"github.com/iter-dev/iter/internal/app"
	iredis "github.com/iter-dev/iter/internal/redis"
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

	deps := app.Deps{
		Logger:       logger,
		BuildVersion: version,
	}

	// Wire Redis when REDIS_URL is set; otherwise log and continue with a
	// nil client. Components that need Redis (ingestion consumer, embed
	// worker, suggest cache fallback) MUST nil-check at use site — see
	// internal/app.Deps.Redis. This soft-fail is deliberately scoped to
	// early bring-up where REDIS_URL may not yet be provisioned; once
	// issue 030 lands the /health endpoint, an unreachable Redis flips
	// the probe to "down" and traffic is shed at the LB.
	if url := os.Getenv("REDIS_URL"); url != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cfg, err := iredis.ConfigFromURL(url)
		if err != nil {
			cancel()
			logger.Error("REDIS_URL is set but invalid; continuing without Redis", "err", err)
		} else if client, err := iredis.NewClient(ctx, cfg); err != nil {
			cancel()
			logger.Error("REDIS_URL is set but ping failed; continuing without Redis", "err", err)
		} else {
			cancel()
			deps.Redis = client
			defer func() {
				if err := client.Close(); err != nil {
					logger.Warn("redis client close", "err", err)
				}
			}()
		}
	} else {
		logger.Warn("REDIS_URL is unset; running without Redis (ingestion / cache disabled)")
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
