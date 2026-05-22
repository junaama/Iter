// Package handler holds the HTTP request handlers mounted by
// internal/api.NewRouter. Each file is a single endpoint or a tightly
// related cluster of endpoints; handlers receive their dependencies via
// app.Deps and stay small enough that the wire shape lives in this file
// next to the handler that emits it.
//
// Issue 030 lands GET /health here. Subsequent endpoint slices
// (suggest, ingest, webhooks, dashboard) land sibling files.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/embed"
	"github.com/iter-dev/iter/internal/llm"
)

// healthBudget is the single shared deadline for every /health probe.
// deploy.md guarantees Railway / BetterStack hit /health every 30s; the
// CLAUDE.md latency budget for suggest is 1s P99 but health is not on
// that path, so 500ms is generous while still safely under the Railway
// 5s healthcheck timeout.
const healthBudget = 500 * time.Millisecond

// processStart is the wall-clock instant this binary booted. The
// uptime_seconds field in the /health envelope is computed as
// time.Since(processStart). Kept package-level (not on app.Deps) so a
// single process has one consistent uptime regardless of how many
// HealthHandler closures are constructed (tests construct several).
var processStart = time.Now()

// healthResponse is the JSON wire shape returned by GET /health.
//
// The order and naming of fields mirrors deploy.md §"Healthcheck"
// verbatim. EmbedRoutes is a v1 extension over deploy.md: 054 landed an
// embedding router with its own breaker state and we surface it next to
// llm_routes so the on-call signal is symmetric across both fan-out
// paths. Both maps default to {} (never nil) so JSON consumers never
// see a `null` field — easier to dashboard.
type healthResponse struct {
	OK            bool                            `json:"ok"`
	Version       string                          `json:"version"`
	DB            string                          `json:"db"`
	Redis         string                          `json:"redis"`
	LLMRoutes     map[string]llm.ProviderStatus   `json:"llm_routes"`
	EmbedRoutes   map[string]embed.ProviderStatus `json:"embed_routes"`
	UptimeSeconds int64                           `json:"uptime_seconds"`
}

// healthProbes bundles the four functions HealthHandler invokes
// concurrently. Extracted so health_test.go can inject deterministic
// stubs without standing up real Postgres / Redis / LLM clients in a
// unit test. Production wiring lives in defaultProbes(deps).
type healthProbes struct {
	db    func(context.Context) error
	redis func(context.Context) error
	llm   func() map[string]llm.ProviderStatus
	embed func() map[string]embed.ProviderStatus
}

// defaultProbes builds the production probe set from app.Deps. Nil deps
// fields short-circuit to a "down" / empty result without touching the
// network — this matches the nil-safety contract spelled out in the 030
// issue body and the app.Deps comments (e.g. Embed/LLM/Redis may be nil
// in smoke builds).
func defaultProbes(deps app.Deps) healthProbes {
	p := healthProbes{}

	if deps.DB == nil {
		p.db = func(context.Context) error { return errors.New("db: not configured") }
	} else {
		pool := deps.DB
		p.db = func(ctx context.Context) error { return db.Healthcheck(ctx, pool) }
	}

	if deps.Redis == nil {
		p.redis = func(context.Context) error { return errors.New("redis: not configured") }
	} else {
		client := deps.Redis
		p.redis = func(ctx context.Context) error {
			return pingRedis(ctx, client)
		}
	}

	if deps.LLM == nil {
		p.llm = func() map[string]llm.ProviderStatus { return map[string]llm.ProviderStatus{} }
	} else {
		router := deps.LLM
		p.llm = func() map[string]llm.ProviderStatus { return router.HealthSnapshot() }
	}

	if deps.Embed == nil {
		p.embed = func() map[string]embed.ProviderStatus { return map[string]embed.ProviderStatus{} }
	} else {
		router := deps.Embed
		p.embed = func() map[string]embed.ProviderStatus { return router.HealthSnapshot() }
	}

	return p
}

// pingRedis runs a single PING with the caller's context. Extracted so
// the goredis call site is in one place; tests inject their own probe
// rather than monkey-patching this helper.
func pingRedis(ctx context.Context, client *goredis.Client) error {
	return client.Ping(ctx).Err()
}

// HealthHandler returns the http.HandlerFunc mounted at GET /health.
//
// Behavior (mirrors deploy.md §"Healthcheck"):
//   - Runs db, redis, llm, and embed probes concurrently under a single
//     500ms deadline. Slow probes are reported as "down" for db/redis
//     and as-snapshot for llm/embed (which never touch the network).
//   - 200 iff db AND redis are "ok"; otherwise 503. LLM/embed status
//     are informational only and never gate the status code, per the
//     binary-stays-up contract in deploy.md.
//   - Empty deps.BuildVersion renders as "dev" so local `go run` builds
//     don't emit an empty string in the JSON.
//
// The handler is intentionally constructed once per router and reused;
// it captures the probes closure and the logger so request hot-path
// allocates only the response envelope.
func HealthHandler(deps app.Deps) http.HandlerFunc {
	return healthHandlerWith(deps, defaultProbes(deps), healthBudget)
}

// healthHandlerWith is the test seam. Same behavior as HealthHandler
// but accepts injected probes and a tunable budget so unit tests can
// assert latency without sleeping for the production 500ms.
func healthHandlerWith(deps app.Deps, probes healthProbes, budget time.Duration) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	version := deps.BuildVersion
	if version == "" {
		version = "dev"
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()

		var (
			wg          sync.WaitGroup
			dbErr       error
			redisErr    error
			llmStatus   map[string]llm.ProviderStatus
			embedStatus map[string]embed.ProviderStatus
		)

		wg.Add(4)
		go func() {
			defer wg.Done()
			dbErr = probes.db(ctx)
		}()
		go func() {
			defer wg.Done()
			redisErr = probes.redis(ctx)
		}()
		go func() {
			defer wg.Done()
			// llm/embed snapshots are pure in-memory reads; we still
			// run them on goroutines so a future "deep probe" mode
			// (issue 030 brief mentions ?deep=1) can swap in a
			// network-touching impl without restructuring the
			// orchestration.
			llmStatus = probes.llm()
		}()
		go func() {
			defer wg.Done()
			embedStatus = probes.embed()
		}()
		wg.Wait()

		resp := healthResponse{
			Version:       version,
			DB:            statusFromErr(dbErr),
			Redis:         statusFromErr(redisErr),
			LLMRoutes:     llmStatus,
			EmbedRoutes:   embedStatus,
			UptimeSeconds: int64(time.Since(processStart).Seconds()),
		}
		if resp.LLMRoutes == nil {
			resp.LLMRoutes = map[string]llm.ProviderStatus{}
		}
		if resp.EmbedRoutes == nil {
			resp.EmbedRoutes = map[string]embed.ProviderStatus{}
		}
		resp.OK = resp.DB == "ok" && resp.Redis == "ok"

		status := http.StatusOK
		if !resp.OK {
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			// Body already partially written; log and move on. Health
			// callers (Railway/BetterStack) only inspect status code,
			// not the trailing bytes.
			logger.WarnContext(r.Context(), "health: encode response failed", "err", err)
		}
	}
}

// statusFromErr is the {nil → "ok", non-nil → "down"} mapping used for
// db and redis. Kept as a tiny helper so the goroutine bodies stay
// linear; also makes it trivial to add a "degraded" middle state later
// (e.g. p99 latency > 100ms) without touching the handler.
func statusFromErr(err error) string {
	if err == nil {
		return "ok"
	}
	return "down"
}
