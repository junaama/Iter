package langfuse

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureLogs returns a slog.Logger that records every record into the
// returned *strings.Builder. Tests use this to assert specific warn
// messages without grepping process stderr.
func captureLogs() (*slog.Logger, *strings.Builder, *sync.Mutex) {
	var buf strings.Builder
	var mu sync.Mutex
	h := slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf, &mu
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

// newTestServer returns a test server that records every batch it receives.
// Callers pass a per-test handler to assert auth / shape; the recorded
// batches are exposed via the returned snapshot func.
func newTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var (
		mu       sync.Mutex
		received []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/ingestion" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, payload)
		mu.Unlock()
		if handler != nil {
			// Re-attach the body for handler inspection if it wants it.
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			handler(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := make([]map[string]any, len(received))
		copy(out, received)
		return out
	}
}

func newGen(name string) Generation {
	now := time.Now()
	return Generation{
		Name:      name,
		StartTime: now.Add(-100 * time.Millisecond),
		EndTime:   now,
		Model:     "test-model",
		Input:     "hello",
		Output:    "world",
		Usage:     Usage{Input: 5, Output: 7, Total: 12},
		Metadata:  map[string]string{"tier": "cheap_hot"},
	}
}

func TestClientPostsBatchWithBasicAuth(t *testing.T) {
	var authSeen string
	srv, snapshot := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	logger, _, _ := captureLogs()
	c, err := NewClient(Config{
		BaseURL:       srv.URL,
		PublicKey:     "pk-test",
		SecretKey:     "sk-test",
		Logger:        logger,
		FlushInterval: 20 * time.Millisecond,
		BatchSize:     8,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	c.Submit(NewGenerationEvent(newGen("anthropic.cheap_hot")))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	batches := snapshot()
	if len(batches) == 0 {
		t.Fatal("server received no batches")
	}
	if !strings.HasPrefix(authSeen, "Basic ") {
		t.Errorf("expected Basic auth header, got %q", authSeen)
	}
	// Expected b64("pk-test:sk-test") = cGstdGVzdDpzay10ZXN0
	if authSeen != "Basic cGstdGVzdDpzay10ZXN0" {
		t.Errorf("auth header mismatch: %q", authSeen)
	}

	// Batch shape: { "batch": [ { id, type, timestamp, body{...} } ] }
	first := batches[0]
	batch, ok := first["batch"].([]any)
	if !ok || len(batch) == 0 {
		t.Fatalf("unexpected batch shape: %#v", first)
	}
	envelope, ok := batch[0].(map[string]any)
	if !ok {
		t.Fatalf("envelope shape: %#v", batch[0])
	}
	if envelope["type"] != "generation-create" {
		t.Errorf("type = %v, want generation-create", envelope["type"])
	}
	body, ok := envelope["body"].(map[string]any)
	if !ok {
		t.Fatalf("body shape: %#v", envelope["body"])
	}
	if body["name"] != "anthropic.cheap_hot" {
		t.Errorf("body.name = %v", body["name"])
	}
	if body["input"] != "hello" || body["output"] != "world" {
		t.Errorf("body.input/output = %v / %v", body["input"], body["output"])
	}
	if body["level"] != "DEFAULT" {
		t.Errorf("body.level = %v, want DEFAULT", body["level"])
	}
}

func TestClientErrorLevelCarriesStatusMessage(t *testing.T) {
	srv, snapshot := newTestServer(t, nil)
	logger, _, _ := captureLogs()
	c, _ := NewClient(Config{
		BaseURL:       srv.URL,
		PublicKey:     "pk",
		SecretKey:     "sk",
		Logger:        logger,
		FlushInterval: 10 * time.Millisecond,
	})

	g := newGen("openai.cheap_hot")
	g.Level = LevelError
	g.StatusMessage = "boom: connection refused"
	g.Output = ""
	c.Submit(NewGenerationEvent(g))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Close(ctx)

	batches := snapshot()
	if len(batches) == 0 {
		t.Fatal("no batches")
	}
	body := batches[0]["batch"].([]any)[0].(map[string]any)["body"].(map[string]any)
	if body["level"] != "ERROR" {
		t.Errorf("level = %v", body["level"])
	}
	if body["statusMessage"] != "boom: connection refused" {
		t.Errorf("statusMessage = %v", body["statusMessage"])
	}
}

func TestClientQueueFullDropsAndLogsWarn(t *testing.T) {
	// A server that hangs forever until released — backs up the worker
	// so the queue fills.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	logger, buf, mu := captureLogs()
	c, _ := NewClient(Config{
		BaseURL:       srv.URL,
		PublicKey:     "pk",
		SecretKey:     "sk",
		Logger:        logger,
		QueueSize:     2,
		FlushInterval: 5 * time.Millisecond,
		BatchSize:     1,
	})

	// Block the worker with the first event, then over-fill.
	// Submit must NEVER block.
	for range 20 {
		done := make(chan struct{})
		go func() {
			c.Submit(NewGenerationEvent(newGen("x")))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Submit blocked — must be non-blocking")
		}
	}

	// At least one drop should have been logged.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	logs := buf.String()
	mu.Unlock()
	if !strings.Contains(logs, "queue full") {
		t.Errorf("expected 'queue full' warn in logs; got: %s", logs)
	}
}

func TestClientFlushesOnClose(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(body, &p)
		if batch, ok := p["batch"].([]any); ok {
			received.Add(int32(len(batch)))
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	logger, _, _ := captureLogs()
	c, _ := NewClient(Config{
		BaseURL:   srv.URL,
		PublicKey: "pk", SecretKey: "sk",
		Logger:        logger,
		FlushInterval: 1 * time.Hour, // ensure timer doesn't fire
		BatchSize:     1000,
	})
	for range 5 {
		c.Submit(NewGenerationEvent(newGen("a")))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := received.Load(); got != 5 {
		t.Errorf("received %d events; want 5", got)
	}
}

func TestClientLogs207ButDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"errors":[{"id":"x","status":400,"message":"bad"}]}`))
	}))
	t.Cleanup(srv.Close)

	logger, buf, mu := captureLogs()
	c, _ := NewClient(Config{
		BaseURL: srv.URL, PublicKey: "pk", SecretKey: "sk",
		Logger:        logger,
		FlushInterval: 10 * time.Millisecond,
	})
	c.Submit(NewGenerationEvent(newGen("a")))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Close(ctx)

	mu.Lock()
	logs := buf.String()
	mu.Unlock()
	if !strings.Contains(logs, "207") && !strings.Contains(logs, "multi-status") {
		t.Errorf("expected 207 warn log, got: %s", logs)
	}
}

func TestConfigFromEnvDisabledWhenUnset(t *testing.T) {
	t.Setenv("LANGFUSE_BASE_URL", "")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	_, err := ConfigFromEnv()
	if err == nil || err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
}

func TestConfigFromEnvEnabledWhenAllSet(t *testing.T) {
	t.Setenv("LANGFUSE_BASE_URL", "https://example.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BaseURL != "https://example.com" || cfg.PublicKey != "pk" || cfg.SecretKey != "sk" {
		t.Errorf("ConfigFromEnv mismatch: %+v", cfg)
	}
}

func TestNilClientSubmitIsSafe(t *testing.T) {
	var c *Client
	// Must not panic.
	c.Submit(Event{Type: "x"})
	if err := c.Close(context.Background()); err != nil {
		t.Errorf("nil Close should return nil, got %v", err)
	}
}

func TestNewClientRejectsInvalidConfig(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Error("empty config should error")
	}
	if _, err := NewClient(Config{BaseURL: "https://x"}); err == nil {
		t.Error("missing keys should error")
	}
}
