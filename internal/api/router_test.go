package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if !strings.Contains(string(body), "skeleton") {
		t.Fatalf("body missing skeleton marker: %q", string(body))
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

func TestServer_TimeoutsAndAddr(t *testing.T) {
	srv := api.NewServer(":0", http.NewServeMux())
	if srv.Addr() != ":0" {
		t.Fatalf("Addr round-trip: got %q want :0", srv.Addr())
	}
}
