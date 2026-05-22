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
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/api"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/archive"
	"github.com/iter-dev/iter/internal/auth"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/embed"
	"github.com/iter-dev/iter/internal/llm"
	iredis "github.com/iter-dev/iter/internal/redis"
	"github.com/iter-dev/iter/internal/ws"
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

	// Boot-time BatchDB pool (iter_batch BYPASSRLS role). Required by
	// the archive cron (issue 047); the Modal nightly scorer owns its
	// own Python-side iter_batch connection so it does NOT consume this
	// pool. When $DATABASE_URL_BATCH is unset we log a warning and
	// leave BatchDB nil — the archive scheduler skips its start in
	// that case so dev boots without R2/cron config still come up.
	batchPool, batchErr := mustNewBatchPool(logger)
	if batchErr != nil {
		// Soft-fail: a missing batch pool is an under-configured deploy,
		// not a fatal misconfiguration of the request path.
		logger.Warn("BatchDB unavailable; archive cron will not start", "err", batchErr)
	} else {
		defer batchPool.Close()
	}

	deps := app.Deps{
		Logger:       logger,
		BuildVersion: version,
		LLM:          buildLLMRouter(logger),
		DB:           pool,
		BatchDB:      batchPool,
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

	// Wire the WorkOS JWT verifier (issue 056). If any of the three
	// required env vars are unset, log a warning and leave deps.Auth
	// nil — the auth middleware (031) nil-checks and returns 503
	// auth_unavailable on every non-whitelisted request so an
	// under-configured deploy is visibly broken instead of silently
	// accepting unauthenticated traffic. Building the verifier does
	// NOT fetch the JWKS (NewVerifier is lazy); the first authenticated
	// request triggers the initial fetch.
	deps.Auth = buildAuthVerifier(logger)

	// Wire the WebSocket gateway (issue 043). The gateway authenticates
	// inside ServeHTTP using deps.Auth, so it must be constructed AFTER
	// buildAuthVerifier. A nil deps.Auth produces a gateway that
	// rejects every upgrade with 503 auth_unavailable, matching the
	// HTTP-middleware posture.
	deps.WS = ws.NewGateway(ws.Config{
		Verifier: deps.Auth,
		Logger:   logger,
	})

	// Wire the AuthKit login flow (GET /auth/login, /auth/callback,
	// /auth/logout). These routes obtain the JWTs that deps.Auth
	// validates. When WORKOS_API_KEY / WORKOS_CLIENT_ID /
	// WORKOS_REDIRECT_URI are unset, AuthKit is nil and the routes
	// are not registered — users authenticate via device-code flow
	// (daemon/CLI) instead.
	deps.AuthKit = buildAuthKit(logger)

	// Webhook shared secrets (issues 041/042). Each source is verified
	// against its own secret so a leak of one doesn't compromise the
	// other. Empty values are accepted at boot but the corresponding
	// handler refuses every delivery with 401 — a wide-open webhook
	// ingress is a security bug, never a fall-open default.
	deps.WebhookSecrets = app.WebhookSecrets{
		GitHub: os.Getenv("GITHUB_WEBHOOK_SECRET"),
		Linear: os.Getenv("LINEAR_WEBHOOK_SECRET"),
	}
	if deps.WebhookSecrets.GitHub == "" {
		logger.Warn("GITHUB_WEBHOOK_SECRET unset; POST /v1/webhooks/github will reject every delivery with 401")
	}
	if deps.WebhookSecrets.Linear == "" {
		logger.Warn("LINEAR_WEBHOOK_SECRET unset; POST /v1/webhooks/linear will reject every delivery with 401")
	}

	// Archive cron (issue 047). Only starts when BatchDB + the full R2
	// configuration are all present; any missing piece is a warning,
	// not a fatal, so non-prod boots without R2 still come up. The
	// scheduler runs in its own goroutine and drains on shutdown via
	// the Stop() call wired into run().
	archiveScheduler := buildArchiveScheduler(logger, deps.BatchDB)
	if archiveScheduler != nil {
		archiveScheduler.Start()
		defer archiveScheduler.Stop()
	}

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

// buildArchiveScheduler wires the daily 03:00 UTC archive cron. Returns
// nil (with a Warn log) when any required piece is missing — BatchDB,
// the R2 credentials, or the Cloudflare API token. Each gate is
// independent so an operator can tell from the boot log exactly which
// env var is missing (vs. a single composite "ARCHIVE_DISABLED" toggle
// that would obscure the actual gap).
//
// CRON SPEC: "0 3 * * *" UTC. The scheduler is constructed with
// time.UTC location (see archive.NewScheduler) so the crontab is
// interpreted in UTC regardless of the server's TZ env var.
func buildArchiveScheduler(logger *slog.Logger, batchDB *pgxpool.Pool) *archive.Scheduler {
	if batchDB == nil {
		logger.Warn("archive cron: BatchDB unset; not starting")
		return nil
	}

	r2cfg := archive.R2Config{
		Endpoint:        os.Getenv("R2_ENDPOINT"),
		AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		Region:          os.Getenv("R2_REGION"),
	}
	bucket := os.Getenv("R2_ARCHIVE_BUCKET")
	if err := r2cfg.Validate(); err != nil || bucket == "" {
		logger.Warn("archive cron: R2 config incomplete; not starting",
			"have_endpoint", r2cfg.Endpoint != "",
			"have_access_key", r2cfg.AccessKeyID != "",
			"have_secret_key", r2cfg.SecretAccessKey != "",
			"have_bucket", bucket != "",
		)
		return nil
	}

	store, err := archive.NewR2Store(context.Background(), r2cfg)
	if err != nil {
		logger.Error("archive cron: NewR2Store failed; not starting", "err", err)
		return nil
	}

	meterCfg := archive.MeterConfig{
		AccountID:     os.Getenv("R2_ACCOUNT_ID"),
		APIToken:      os.Getenv("CLOUDFLARE_API_TOKEN"),
		BucketName:    bucket,
		FreeStorageGB: parseFloatEnv("R2_FREE_STORAGE_GB", 10),
		FreeClassAOps: parseInt64Env("R2_FREE_CLASS_A_OPS", 1_000_000),
		FreeClassBOps: parseInt64Env("R2_FREE_CLASS_B_OPS", 10_000_000),
	}
	meter, err := archive.NewCloudflareMeter(meterCfg)
	if err != nil {
		logger.Warn("archive cron: meter config incomplete; not starting", "err", err)
		return nil
	}

	sch, err := archive.NewScheduler(archive.SchedulerConfig{
		Spec: "0 3 * * *", // 03:00 UTC daily; ARCHITECTURE.md §4
		Cron: archive.Config{
			BatchDB:        batchDB,
			Store:          store,
			Bucket:         bucket,
			Meter:          meter,
			AlertThreshold: parseFloatEnv("R2_USAGE_ALERT_THRESHOLD", 0.80),
			Logger:         logger,
		},
		Logger: logger,
	})
	if err != nil {
		logger.Error("archive cron: scheduler construction failed", "err", err)
		return nil
	}
	logger.Info("archive cron: scheduled", "spec", "0 3 * * *", "bucket", bucket)
	return sch
}

// parseFloatEnv reads a float env var, returning fallback on missing or
// unparsable. Used for the R2 free-tier numeric overrides so an
// operator can shrink the staging guardrail without touching code.
func parseFloatEnv(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := parseFloat(raw)
	if err != nil {
		return fallback
	}
	return v
}

// parseInt64Env mirrors parseFloatEnv for the Class A/B ops counters.
func parseInt64Env(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := parseInt64(raw)
	if err != nil {
		return fallback
	}
	return v
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

// buildAuthVerifier constructs the WorkOS JWT verifier from environment
// variables. Returns nil (with a Warn log) when any of WORKOS_JWKS_URL,
// WORKOS_ISSUER, or WORKOS_AUDIENCE are unset, so early-bring-up boots
// before WorkOS is provisioned still come up — the auth middleware
// (issue 031) nil-checks and returns 503 auth_unavailable on every
// non-whitelisted request in that state, which is the intended visible-
// broken posture for an under-configured deploy.
//
// NewVerifier does NOT fetch the JWKS; the first authenticated request
// triggers the initial fetch with a synchronous round-trip that the
// verifier's stale-while-revalidate cache amortizes for subsequent
// requests (1h fresh TTL + 10m stale window).
func buildAuthVerifier(logger *slog.Logger) *auth.Verifier {
	jwksURL := os.Getenv("WORKOS_JWKS_URL")
	issuer := os.Getenv("WORKOS_ISSUER")
	audience := os.Getenv("WORKOS_AUDIENCE")
	if jwksURL == "" || issuer == "" || audience == "" {
		logger.Warn(
			"WORKOS_* env vars incomplete; auth middleware will return 503 auth_unavailable on every authenticated request",
			"have_jwks_url", jwksURL != "",
			"have_issuer", issuer != "",
			"have_audience", audience != "",
		)
		return nil
	}
	v, err := auth.NewVerifier(auth.VerifierConfig{
		JWKSURL:  jwksURL,
		Issuer:   issuer,
		Audience: audience,
	})
	if err != nil {
		// NewVerifier only errors on missing required fields, which
		// we already checked above; any error here is a programming
		// bug rather than a config issue, but we still soft-fail so
		// the server boots and the middleware returns 503.
		logger.Error("failed to construct auth verifier; continuing with deps.Auth=nil", "err", err)
		return nil
	}
	return v
}

// buildAuthKit constructs the WorkOS AuthKit handler from environment
// variables. Returns nil (with a Warn log) when any of WORKOS_API_KEY,
// WORKOS_CLIENT_ID, or WORKOS_REDIRECT_URI are unset, so early-bring-up
// boots before WorkOS is fully provisioned still come up — the login
// routes are simply not registered, and users authenticate via
// device-code flow (daemon/CLI) instead.
func buildAuthKit(logger *slog.Logger) *auth.AuthKit {
	cfg := auth.AuthKitConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		logger.Warn(
			"WORKOS_* env vars incomplete for AuthKit; login routes will not be registered",
			"have_api_key", cfg.APIKey != "",
			"have_client_id", cfg.ClientID != "",
			"have_redirect_uri", cfg.RedirectURI != "",
		)
		return nil
	}
	cfg.Logger = logger

	ak, err := auth.NewAuthKit(cfg)
	if err != nil {
		// NewAuthKit only errors on missing required fields, which
		// we already checked above; any error here is a programming
		// bug rather than a config issue.
		logger.Error("failed to construct AuthKit; continuing without login routes", "err", err)
		return nil
	}
	logger.Info("AuthKit login routes enabled",
		"redirect_uri", cfg.RedirectURI,
	)
	return ak
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

// mustNewBatchPool reads $DATABASE_URL_BATCH and builds the BYPASSRLS
// iter_batch pool. Returns (nil, error) when the env var is unset OR
// when the connection fails. Unlike mustNewPool, the binary tolerates a
// missing DATABASE_URL_BATCH: the request path is fully usable without
// it; the archive cron simply doesn't start.
func mustNewBatchPool(logger *slog.Logger) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL_BATCH")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL_BATCH unset")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbStartupTimeout)
	defer cancel()
	return db.NewPool(ctx, db.PoolConfig{
		DSN:    dsn,
		Logger: logger,
	})
}

// parseFloat is the strconv.ParseFloat shim used by the R2 env parsers.
// Centralized so the call sites in buildArchiveScheduler stay one-liner.
func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

// parseInt64 is the strconv.ParseInt(64) shim, mirroring parseFloat.
func parseInt64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
