// Command server is the single-binary cloud process for Iter.
//
// As of issue 028 it boots the chi router under a *http.Server with the
// documented timeouts and the full dependency bag. The middleware chain
// (request_id → logger → recover → auth → tenant_context → rate_limit →
// idempotency) and handler tree land in subsequent slices (029–047). Until
// then the router only registers a NotFound handler that returns 503 so
// any traffic hitting this binary is loud, not silent.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/api"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/embed"
	"github.com/iter-dev/iter/internal/llm"
	iredis "github.com/iter-dev/iter/internal/redis"
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
		LLM:          buildLLMRouter(logger),
		DB:           pool,
		// BatchDB left nil — Modal worker (issue 046) owns its own
		// iter_batch connection.
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

	// Wire the embedding router. Built after Redis so it can consume the
	// shared client for the SHA256 cache. A nil Redis is acceptable —
	// NewCache returns a nil *Cache and the router runs cache-disabled.
	deps.Embed = buildEmbedRouter(logger, deps.Redis)

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	srv := api.NewServer(":"+port, api.NewRouter(deps))

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

// buildEmbedRouter constructs the embedding router from environment vars.
// Only providers whose API key env var is set are registered; missing keys
// are tolerated (non-prod boots) — the router falls through to the next
// provider in the chain at request time.
//
// Provider priority chain (DECISIONS.md "Embedding provider chain
// (issue 054)"):
//
//	voyage → openai → google
//
// Voyage is the v1 default (voyage-code-3, 1536-dim, matches the
// session_embeddings.embedding vector(1536) column). OpenAI and Google
// are stubs today, registered so the chain is wired and ready when their
// HTTP implementations land. Breaker tuning matches LLM defaults (5
// failures, 30s recovery).
//
// The shared *goredis.Client is passed through for the SHA256 cache; a
// nil client is acceptable (cache-disabled in dev).
func buildEmbedRouter(logger *slog.Logger, rdb embed.RedisLike) *embed.Router {
	var providers []embed.Provider

	if key := os.Getenv("VOYAGE_API_KEY"); key != "" {
		providers = append(providers, embed.NewVoyageProvider(embed.VoyageConfig{APIKey: key}))
	} else {
		logger.Warn("VOYAGE_API_KEY not set; voyage provider unregistered (embedding chain has no real provider)")
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		providers = append(providers, embed.NewOpenAIProvider(embed.OpenAIConfig{APIKey: key}))
	}
	if key := os.Getenv("GOOGLE_AI_API_KEY"); key != "" {
		providers = append(providers, embed.NewGoogleProvider(embed.GoogleConfig{APIKey: key}))
	}

	var cache *embed.Cache
	if rdb != nil {
		cache = embed.NewCache(embed.CacheConfig{Redis: rdb})
	}

	return embed.NewRouter(embed.RouterConfig{
		Providers: providers,
		Priority:  []string{"voyage", "openai", "google"},
		Cache:     cache,
	})
}

// run is split out so it can be unit-tested without exiting the process.
// It blocks until a SIGTERM/SIGINT arrives, then drains within
// shutdownTimeout and returns.
func run(srv *api.Server, deps app.Deps) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		deps.Logger.Info("server listening",
			"addr", srv.Addr(),
			"version", deps.BuildVersion,
		)
		errCh <- srv.Run()
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
	// Drain the Run goroutine so callers don't leak it.
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
