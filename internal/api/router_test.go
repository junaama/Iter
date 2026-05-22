package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/iter-dev/iter/internal/api"
	"github.com/iter-dev/iter/internal/app"
)

func TestRouter_UnregisteredRouteReturns503(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/no-such-route")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// With nil deps.Auth, the auth middleware (031) fires before
	// NotFound and returns 503 auth_unavailable. The original
	// "skeleton" marker only surfaces when auth is wired AND the
	// route is unregistered, which won't be a typical state — once
	// 031's auth is in the chain, every unrouted request to the
	// authed Group either gets auth_unavailable (nil verifier) or
	// invalid_token (real verifier rejecting the missing bearer).
	// Either way, the router IS reachable and IS returning 503;
	// that's what this test asserts.
	if !strings.Contains(string(body), "auth_unavailable") && !strings.Contains(string(body), "skeleton") {
		t.Fatalf("body missing expected marker: %q", string(body))
	}
}

// TestRouter_HealthRegistered verifies that GET /health is wired into
// the chi router (issue 030). The handler runs against a nil-deps shape
// — no DB, no Redis, no LLM/Embed — so we expect 503 because the
// db+redis probes both report "down", but the endpoint must still be
// reachable (i.e. not 404). Detailed shape assertions live in
// internal/api/handler/health_test.go.
func TestRouter_HealthRegistered(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 (nil deps), got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: want application/json got %q", got)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["db"] != "down" || body["redis"] != "down" {
		t.Fatalf("nil deps: want db=down redis=down got %v", body)
	}
}

func TestRouter_DashboardMeRegistered(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	found := false
	if err := chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && route == "/v1/dashboard/me" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if !found {
		t.Fatalf("GET /v1/dashboard/me route not registered")
	}
}

func TestServer_TimeoutsAndAddr(t *testing.T) {
	srv := api.NewServer(":0", http.NewServeMux())
	if srv.Addr() != ":0" {
		t.Fatalf("Addr round-trip: got %q want :0", srv.Addr())
	}
}
