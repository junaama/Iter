package embed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoyageNotConfiguredWithoutKey(t *testing.T) {
	p := NewVoyageProvider(VoyageConfig{})
	_, err := p.Embed(context.Background(), EmbedRequest{Inputs: []string{"x"}})
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("got %v, want ErrProviderNotConfigured", err)
	}
}

func TestVoyageSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing/wrong auth header: %s", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var req voyageRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		if req.Model != "voyage-code-3" {
			t.Errorf("model = %q, want voyage-code-3", req.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		// Note: indices intentionally out of order to verify the
		// provider reorders by Data[i].Index.
		_, _ = io.WriteString(w, `{
			"data": [
				{"index": 1, "embedding": [4, 5, 6]},
				{"index": 0, "embedding": [1, 2, 3]}
			],
			"model": "voyage-code-3",
			"usage": {"total_tokens": 11}
		}`)
	}))
	defer srv.Close()

	p := NewVoyageProvider(VoyageConfig{APIKey: "test-key", BaseURL: srv.URL})
	resp, err := p.Embed(context.Background(), EmbedRequest{Inputs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Vectors) != 2 {
		t.Fatalf("got %d vectors, want 2", len(resp.Vectors))
	}
	if resp.Vectors[0][0] != 1 || resp.Vectors[1][0] != 4 {
		t.Errorf("vectors not reordered by index: %+v", resp.Vectors)
	}
	if resp.TokensIn != 11 {
		t.Errorf("tokens in = %d, want 11", resp.TokensIn)
	}
}

func TestVoyageNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()

	p := NewVoyageProvider(VoyageConfig{APIKey: "k", BaseURL: srv.URL})
	_, err := p.Embed(context.Background(), EmbedRequest{Inputs: []string{"x"}})
	if err == nil {
		t.Fatal("expected error on non-2xx")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status code; got %v", err)
	}
}

func TestVoyageEmptyInputs(t *testing.T) {
	p := NewVoyageProvider(VoyageConfig{APIKey: "k"})
	_, err := p.Embed(context.Background(), EmbedRequest{Inputs: nil})
	if err == nil {
		t.Fatal("empty inputs should error")
	}
}
