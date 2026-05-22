package api_test

import (
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

func TestServer_TimeoutsAndAddr(t *testing.T) {
	srv := api.NewServer(":0", http.NewServeMux())
	if srv.Addr() != ":0" {
		t.Fatalf("Addr round-trip: got %q want :0", srv.Addr())
	}
}
