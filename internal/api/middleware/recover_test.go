package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iter-dev/iter/internal/api/middleware"
)

func TestRecover_PanicReturns500GenericBody(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	chain := middleware.Chain(middleware.RequestID, middleware.Recover(log))
	h := chain(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom: secret=hunter2")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/explode", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != `{"error":"internal"}` {
		t.Fatalf("body: got %q want generic", body)
	}
	if strings.Contains(body, "kaboom") || strings.Contains(body, "hunter2") {
		t.Fatalf("panic value leaked into response body: %q", body)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header missing on panic response")
	}

	// Inspect the log: stack should land here, NOT in the response.
	if buf.Len() == 0 {
		t.Fatal("no log lines emitted")
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if entry["level"] != "ERROR" {
		t.Errorf("level: got %v want ERROR", entry["level"])
	}
	if entry["msg"] != "http_panic" {
		t.Errorf("msg: got %v want http_panic", entry["msg"])
	}
	stack, _ := entry["stack"].(string)
	if !strings.Contains(stack, "runtime/debug.Stack") && !strings.Contains(stack, "goroutine") {
		t.Errorf("stack missing runtime markers: %q", stack)
	}
	if entry["request_id"] == "" || entry["request_id"] == nil {
		t.Error("log missing request_id")
	}
	if entry["panic"] != "kaboom: secret=hunter2" {
		t.Errorf("panic value: got %v", entry["panic"])
	}
}

func TestRecover_NoPanicPassesThrough(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))

	h := middleware.Recover(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body: %q", rec.Body.String())
	}
	if buf.Len() != 0 {
		t.Fatalf("Recover logged on happy path: %s", buf.String())
	}
}

func TestRecover_AbortHandlerRepanics(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))

	h := middleware.Recover(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		rv := recover()
		if rv != http.ErrAbortHandler {
			t.Fatalf("expected http.ErrAbortHandler to repanic, got %v", rv)
		}
		if buf.Len() != 0 {
			t.Errorf("ErrAbortHandler should not log: %s", buf.String())
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("expected panic to propagate")
}
