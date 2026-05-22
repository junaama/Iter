package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/embed"
	"github.com/iter-dev/iter/internal/llm"
)

// newTestDeps builds an app.Deps with a discard logger so handler
// constructors don't blow up on a nil logger. Concrete probe behavior
// is injected via healthHandlerWith in each test rather than via deps.
func newTestDeps(version string) app.Deps {
	return app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: version,
	}
}

// invoke runs the handler against a synthetic request and returns the
// status code + decoded body. Centralized so each test stays a
// one-liner per assertion.
func invoke(t *testing.T, h http.HandlerFunc) (int, healthResponse, http.Header) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return rec.Code, body, rec.Header()
}

func TestHealthHandler_AllGreen(t *testing.T) {
	probes := healthProbes{
		db:    func(context.Context) error { return nil },
		redis: func(context.Context) error { return nil },
		llm: func() map[string]llm.ProviderStatus {
			return map[string]llm.ProviderStatus{"anthropic": llm.StatusOK}
		},
		embed: func() map[string]embed.ProviderStatus {
			return map[string]embed.ProviderStatus{"voyage": embed.StatusOK}
		},
	}
	h := healthHandlerWith(newTestDeps("0.4.2"), probes, 50*time.Millisecond)

	code, body, hdr := invoke(t, h)
	if code != http.StatusOK {
		t.Fatalf("status: want 200 got %d", code)
	}
	if !body.OK {
		t.Fatalf("ok: want true got false")
	}
	if body.DB != "ok" || body.Redis != "ok" {
		t.Fatalf("db/redis: want ok/ok got %s/%s", body.DB, body.Redis)
	}
	if body.Version != "0.4.2" {
		t.Fatalf("version: want 0.4.2 got %q", body.Version)
	}
	if body.LLMRoutes["anthropic"] != llm.StatusOK {
		t.Fatalf("llm anthropic: want ok got %q", body.LLMRoutes["anthropic"])
	}
	if body.EmbedRoutes["voyage"] != embed.StatusOK {
		t.Fatalf("embed voyage: want ok got %q", body.EmbedRoutes["voyage"])
	}
	if got := hdr.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: want application/json got %q", got)
	}
}

func TestHealthHandler_DBDown(t *testing.T) {
	probes := healthProbes{
		db:    func(context.Context) error { return errors.New("connection refused") },
		redis: func(context.Context) error { return nil },
		llm:   func() map[string]llm.ProviderStatus { return nil },
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps("test"), probes, 50*time.Millisecond)

	code, body, _ := invoke(t, h)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503 got %d", code)
	}
	if body.OK {
		t.Fatalf("ok: want false")
	}
	if body.DB != "down" {
		t.Fatalf("db: want down got %q", body.DB)
	}
	if body.Redis != "ok" {
		t.Fatalf("redis: want ok got %q", body.Redis)
	}
}

func TestHealthHandler_RedisDown(t *testing.T) {
	probes := healthProbes{
		db:    func(context.Context) error { return nil },
		redis: func(context.Context) error { return errors.New("no route to host") },
		llm:   func() map[string]llm.ProviderStatus { return nil },
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps("test"), probes, 50*time.Millisecond)

	code, body, _ := invoke(t, h)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503 got %d", code)
	}
	if body.Redis != "down" {
		t.Fatalf("redis: want down got %q", body.Redis)
	}
}

func TestHealthHandler_LLMDegradedStillOK(t *testing.T) {
	probes := healthProbes{
		db:    func(context.Context) error { return nil },
		redis: func(context.Context) error { return nil },
		llm: func() map[string]llm.ProviderStatus {
			return map[string]llm.ProviderStatus{
				"anthropic": llm.StatusOK,
				"openai":    llm.StatusOK,
				"google":    llm.StatusDegraded,
			}
		},
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps("test"), probes, 50*time.Millisecond)

	code, body, _ := invoke(t, h)
	if code != http.StatusOK {
		t.Fatalf("status: want 200 (llm degraded is informational) got %d", code)
	}
	if !body.OK {
		t.Fatalf("ok: want true (db+redis healthy) got false")
	}
	if body.LLMRoutes["google"] != llm.StatusDegraded {
		t.Fatalf("google: want degraded got %q", body.LLMRoutes["google"])
	}
}

func TestHealthHandler_AllLLMDownStill200(t *testing.T) {
	probes := healthProbes{
		db:    func(context.Context) error { return nil },
		redis: func(context.Context) error { return nil },
		llm: func() map[string]llm.ProviderStatus {
			return map[string]llm.ProviderStatus{
				"anthropic": llm.StatusDown,
				"openai":    llm.StatusDown,
			}
		},
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps("test"), probes, 50*time.Millisecond)

	code, _, _ := invoke(t, h)
	if code != http.StatusOK {
		t.Fatalf("status: want 200 (all LLMs down is informational) got %d", code)
	}
}

func TestHealthHandler_ResponseShape(t *testing.T) {
	probes := healthProbes{
		db:    func(context.Context) error { return nil },
		redis: func(context.Context) error { return nil },
		llm:   func() map[string]llm.ProviderStatus { return nil },
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps(""), probes, 50*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantKeys := []string{"ok", "version", "db", "redis", "llm_routes", "embed_routes", "uptime_seconds"}
	for _, k := range wantKeys {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing key %q in response: %v", k, raw)
		}
	}
	// Empty BuildVersion renders as "dev".
	var version string
	if err := json.Unmarshal(raw["version"], &version); err != nil {
		t.Fatalf("version unmarshal: %v", err)
	}
	if version != "dev" {
		t.Fatalf("empty version: want dev got %q", version)
	}
	// Nil llm/embed maps must serialize as {}, never null — dashboards
	// rely on the field being an object.
	if string(raw["llm_routes"]) != "{}" {
		t.Fatalf("llm_routes: want {} got %s", raw["llm_routes"])
	}
	if string(raw["embed_routes"]) != "{}" {
		t.Fatalf("embed_routes: want {} got %s", raw["embed_routes"])
	}
}

func TestHealthHandler_LatencyUnder100ms(t *testing.T) {
	// All probes return instantly; we still wrap in goroutines so the
	// scheduler has to fan out + join. Budget is 50ms; assertion is
	// well above that to allow for CI noise but well under 100ms.
	probes := healthProbes{
		db:    func(context.Context) error { return nil },
		redis: func(context.Context) error { return nil },
		llm:   func() map[string]llm.ProviderStatus { return nil },
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps("test"), probes, 50*time.Millisecond)

	start := time.Now()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("latency: want <100ms got %v", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d", rec.Code)
	}
}

func TestHealthHandler_BudgetEnforced(t *testing.T) {
	// DB probe respects the injected ctx deadline; if it doesn't fire
	// in time we want the handler to treat it as down rather than
	// hang. Use a very short budget and a probe that blocks until
	// ctx is done, returning the ctx error.
	probes := healthProbes{
		db: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		redis: func(context.Context) error { return nil },
		llm:   func() map[string]llm.ProviderStatus { return nil },
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps("test"), probes, 20*time.Millisecond)

	start := time.Now()
	code, body, _ := invoke(t, h)
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Fatalf("budget: handler ran %v, expected ~20ms", elapsed)
	}
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503 (db deadline exceeded) got %d", code)
	}
	if body.DB != "down" {
		t.Fatalf("db: want down got %q", body.DB)
	}
}

func TestHealthHandler_NilDepsAllDown(t *testing.T) {
	// app.Deps with no DB/Redis/LLM/Embed — defaultProbes must yield
	// db=down, redis=down, empty llm/embed maps, and 503.
	deps := newTestDeps("test")
	h := HealthHandler(deps)

	code, body, _ := invoke(t, h)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503 got %d", code)
	}
	if body.DB != "down" || body.Redis != "down" {
		t.Fatalf("want down/down got %s/%s", body.DB, body.Redis)
	}
	if body.LLMRoutes == nil || len(body.LLMRoutes) != 0 {
		t.Fatalf("llm_routes: want empty map got %v", body.LLMRoutes)
	}
	if body.EmbedRoutes == nil || len(body.EmbedRoutes) != 0 {
		t.Fatalf("embed_routes: want empty map got %v", body.EmbedRoutes)
	}
}

func TestHealthHandler_UptimeMonotonic(t *testing.T) {
	// Two back-to-back calls — uptime_seconds is non-decreasing. We
	// don't assert strictly greater because the second call can land
	// in the same second.
	probes := healthProbes{
		db:    func(context.Context) error { return nil },
		redis: func(context.Context) error { return nil },
		llm:   func() map[string]llm.ProviderStatus { return nil },
		embed: func() map[string]embed.ProviderStatus { return nil },
	}
	h := healthHandlerWith(newTestDeps("test"), probes, 50*time.Millisecond)

	_, first, _ := invoke(t, h)
	_, second, _ := invoke(t, h)

	if second.UptimeSeconds < first.UptimeSeconds {
		t.Fatalf("uptime decreased: first=%d second=%d", first.UptimeSeconds, second.UptimeSeconds)
	}
	if first.UptimeSeconds < 0 {
		t.Fatalf("uptime negative: %d", first.UptimeSeconds)
	}
}
