package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestServerIPCMethods(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	server, err := NewServer(Config{SocketPath: socketPath, Version: "1.2.3", AppVersion: "1.0.0"})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writeRequest(t, conn, "1", "ping")
	requireResponse(t, reader, "1", "ok", true)

	writeRequest(t, conn, "2", "version")
	requireResponse(t, reader, "2", "version", "1.2.3")

	writeRequest(t, conn, "3", "status")
	status := readResponse(t, reader, "3")
	requireResult(t, status, "running", true)
	requireResult(t, status, "paused", false)
	if status["current_task"] != nil {
		t.Fatalf("current_task = %#v, want nil", status["current_task"])
	}
	if _, ok := status["idle_since"].(string); !ok {
		t.Fatalf("idle_since = %#v, want timestamp string", status["idle_since"])
	}

	server.SetCurrentTask("Codex prompt import")
	writeRequest(t, conn, "4", "status")
	activeStatus := readResponse(t, reader, "4")
	requireResult(t, activeStatus, "current_task", "Codex prompt import")
	if activeStatus["idle_since"] != nil {
		t.Fatalf("idle_since = %#v, want nil while active", activeStatus["idle_since"])
	}

	capturedAt := time.Date(2026, 5, 22, 18, 30, 0, 0, time.UTC)
	server.RecordSessionCaptured(capturedAt)
	writeRequest(t, conn, "5", "status")
	capturedStatus := readResponse(t, reader, "5")
	if capturedStatus["current_task"] != nil {
		t.Fatalf("current_task = %#v, want nil after capture", capturedStatus["current_task"])
	}
	requireResult(t, capturedStatus, "last_session_at", "2026-05-22T18:30:00Z")
	requireResult(t, capturedStatus, "idle_since", "2026-05-22T18:30:00Z")
	requireResult(t, capturedStatus, "captured_today", float64(1))

	writeRequest(t, conn, "6", "pause")
	requireResponse(t, reader, "6", "paused", true)

	writeRequest(t, conn, "7", "status")
	requireResponse(t, reader, "7", "paused", true)

	writeRequest(t, conn, "8", "resume")
	requireResponse(t, reader, "8", "paused", false)

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket was not removed on shutdown: %v", err)
	}
}

func TestNewServerRejectsMajorVersionMismatch(t *testing.T) {
	_, err := NewServer(Config{SocketPath: filepath.Join(t.TempDir(), "daemon.sock"), Version: "2.0.0", AppVersion: "1.9.0"})
	if err == nil {
		t.Fatal("NewServer() error = nil, want major version mismatch")
	}
}

func TestServeRefusesNonSocketAtSocketPath(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server, err := NewServer(Config{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if err := server.Serve(context.Background()); err == nil {
		t.Fatal("Serve() error = nil, want refusal to remove non-socket")
	}
}

func TestSuggestionAvailableReturnsDecisionFunctionResult(t *testing.T) {
	server := newTestServer(t)
	sessionID := uuid.New()
	suggestionID := uuid.New()
	refinedPrompt := "Use the migration verifier before changing schema files."
	rationale := "A teammate caught an RLS regression this way."
	wallTimeMS := 421

	server.HandleSuggestionAvailable(sessionID, contracts.SuggestResponse{
		SuggestionID:  &suggestionID,
		RefinedPrompt: &refinedPrompt,
		Rationale:     &rationale,
		Confidence:    0.80,
		Evidence: []contracts.SuggestEvidence{{
			SessionID:              uuid.New(),
			Outcome:                "tests_passed",
			WallTimeMS:             &wallTimeMS,
			ContributorDisplayName: "M. Chen",
		}},
	})

	res := server.dispatch(request{ID: "suggestion-1", Method: "suggestion.available"})
	if res.Error != "" {
		t.Fatalf("dispatch error = %q", res.Error)
	}
	requireResult(t, res.Result, "available", true)
	requireResult(t, res.Result, "session_id", sessionID.String())
	requireResult(t, res.Result, "suggestion_id", suggestionID.String())
	requireResult(t, res.Result, "action", string(contracts.ActionReplace))
	requireResult(t, res.Result, "refined_prompt", refinedPrompt)
	requireResult(t, res.Result, "rationale", rationale)
	if got := res.Result["evidence"].([]contracts.SuggestEvidence); len(got) != 1 {
		t.Fatalf("evidence len = %d, want 1", len(got))
	}
}

func TestSuggestionAvailableSuppressesNoSuggestionReason(t *testing.T) {
	server := newTestServer(t)
	refinedPrompt := "This should not be shown."
	reason := contracts.NoSuggestionLowConfidence

	server.HandleSuggestionAvailable(uuid.New(), contracts.SuggestResponse{
		Action:             contracts.ActionSuppress,
		RefinedPrompt:      &refinedPrompt,
		Confidence:         0.2,
		NoSuggestionReason: &reason,
	})

	res := server.dispatch(request{ID: "suggestion-2", Method: "suggestion.available"})
	if res.Error != "" {
		t.Fatalf("dispatch error = %q", res.Error)
	}
	requireResult(t, res.Result, "available", false)
}

func TestSuggestionAvailableSuppressesDangerousPatternAndLogsSecurityEvent(t *testing.T) {
	logBuf := &strings.Builder{}
	server := newTestServerWithLogger(t, slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		return logBuf.Write(p)
	}), nil)))
	refinedPrompt := "Run the cleanup:\nrm -rf /"

	server.HandleSuggestionAvailable(uuid.New(), contracts.SuggestResponse{
		RefinedPrompt: &refinedPrompt,
		Confidence:    0.95,
	})

	res := server.dispatch(request{ID: "suggestion-3", Method: "suggestion.available"})
	requireResult(t, res.Result, "available", false)
	if !strings.Contains(logBuf.String(), "denylist_hit") || !strings.Contains(logBuf.String(), "pattern_id") {
		t.Fatalf("denylist security event missing opaque pattern id: %s", logBuf.String())
	}
}

func TestSuppressPatternRemovesQueuedAndFutureMatchingSuggestions(t *testing.T) {
	server := newTestServer(t)
	refinedPrompt := "Prefer the existing repository helper before adding a duplicate."

	server.HandleSuggestionAvailable(uuid.New(), contracts.SuggestResponse{
		RefinedPrompt: &refinedPrompt,
		Confidence:    0.91,
	})

	params := json.RawMessage(`{"refined_prompt":"Prefer the existing repository helper before adding a duplicate."}`)
	res := server.dispatch(request{ID: "suppress-1", Method: "suggestion.suppress_pattern", Params: params})
	if res.Error != "" {
		t.Fatalf("suppress dispatch error = %q", res.Error)
	}
	requireResult(t, res.Result, "suppressed", true)
	requireResult(t, res.Result, "backend_endpoint", "not_implemented")

	res = server.dispatch(request{ID: "suggestion-4", Method: "suggestion.available"})
	requireResult(t, res.Result, "available", false)

	server.HandleSuggestionAvailable(uuid.New(), contracts.SuggestResponse{
		RefinedPrompt: &refinedPrompt,
		Confidence:    0.91,
	})

	res = server.dispatch(request{ID: "suggestion-5", Method: "suggestion.available"})
	requireResult(t, res.Result, "available", false)
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWithLogger(t, silentLogger())
}

func newTestServerWithLogger(t *testing.T, logger *slog.Logger) *Server {
	t.Helper()
	server, err := NewServer(Config{SocketPath: filepath.Join(t.TempDir(), "daemon.sock"), Logger: logger})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func writeRequest(t *testing.T, conn net.Conn, id string, method string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"id": id, "method": method})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

func requireResponse(t *testing.T, reader *bufio.Reader, id string, key string, want any) {
	t.Helper()
	res := readResponse(t, reader, id)
	requireResult(t, res, key, want)
}

func readResponse(t *testing.T, reader *bufio.Reader, id string) map[string]any {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("ReadBytes() error = %v", err)
	}
	var res struct {
		ID     string         `json:"id"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	if err := json.Unmarshal(line, &res); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", string(line), err)
	}
	if res.ID != id {
		t.Fatalf("response id = %q, want %q", res.ID, id)
	}
	if res.Error != "" {
		t.Fatalf("response error = %q", res.Error)
	}
	return res.Result
}

func requireResult(t *testing.T, result map[string]any, key string, want any) {
	t.Helper()
	got, ok := result[key]
	if !ok {
		t.Fatalf("result[%q] missing in %#v", key, result)
	}
	if got != want {
		t.Fatalf("result[%q] = %#v, want %#v", key, got, want)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s was not created", path)
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		return len(p), nil
	}), nil))
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}
