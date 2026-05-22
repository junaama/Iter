package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/iter-dev/iter/internal/api"
	"github.com/iter-dev/iter/internal/app"
)

func TestRouter_LinearWebhookRegistered(t *testing.T) {
	deps := app.Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BuildVersion: "test",
	}

	r := api.NewRouter(deps)
	found := false
	if err := chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodPost && route == "/v1/webhooks/linear" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if !found {
		t.Fatalf("POST /v1/webhooks/linear route not registered")
	}
}
