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
	"github.com/iter-dev/iter/internal/llm"
	"github.com/iter-dev/iter/pkg/contracts"
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
		LLM:          buildLLMRouter(logger),
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

// buildLLMRouter constructs the per-tier LLM router from environment vars.
// Only providers whose API key env var is set are registered; missing keys
// are tolerated (non-prod boots) — the router falls through to the next
// provider in the chain at request time.
//
// Provider priority chain per tier (DECISIONS.md "LLM provider chain
// (issue 055)"):
//
//	cheap_hot: anthropic → google → openai → together
//	sonnet:    anthropic → openai
//	opus:      anthropic
//
// Breaker tuning is the v1 default (5 consecutive failures → open;
// 30s recovery delay → half-open).
func buildLLMRouter(logger *slog.Logger) *llm.Router {
	var providers []llm.Provider

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		providers = append(providers, llm.NewAnthropicProvider(llm.AnthropicConfig{APIKey: key}))
	} else {
		logger.Warn("ANTHROPIC_API_KEY not set; anthropic provider unregistered")
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		providers = append(providers, llm.NewOpenAIProvider(llm.OpenAIConfig{APIKey: key}))
	}
	if key := os.Getenv("GOOGLE_AI_API_KEY"); key != "" {
		providers = append(providers, llm.NewGoogleProvider(llm.GoogleConfig{APIKey: key}))
	}
	if key := os.Getenv("TOGETHER_API_KEY"); key != "" {
		providers = append(providers, llm.NewTogetherProvider(llm.TogetherConfig{APIKey: key}))
	}

	// Declared chain regardless of which keys are present. The router
	// silently skips any name that wasn't registered above ("unregistered"
	// in the attempts log).
	priority := map[contracts.LLMTier][]string{
		contracts.LLMTierCheapHot: {"anthropic", "google", "openai", "together"},
		contracts.LLMTierSonnet:   {"anthropic", "openai"},
		contracts.LLMTierOpus:     {"anthropic"},
	}

	return llm.NewRouter(llm.RouterConfig{
		Providers: providers,
		Priority:  priority,
	})
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
