package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestCaptureRunnerScansJSONLSession(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(root, "session.jsonl")
	body := strings.Join([]string{
		`{"timestamp":"2026-05-23T11:58:00Z","model":"gpt-5","message":{"role":"user","content":"ship api_key=super-secret thing"},"tool":"read"}`,
		`{"completed_at":"2026-05-23T11:59:00Z","tool":"edit"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	oldMTime := now.Add(-time.Minute)
	if err := os.Chtimes(path, oldMTime, oldMTime); err != nil {
		t.Fatal(err)
	}

	runner := NewCaptureRunner(CaptureConfig{
		APIToken:   "token",
		WSEndpoint: "ws://example.test/v1/ws",
		Dirs:       []HarnessDir{{Harness: "codex", Path: root}},
		Now:        func() time.Time { return now },
	}, nil, slog.Default())
	events, err := runner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].EventType != contracts.EventPromptSent {
		t.Fatalf("first event = %s, want prompt_sent", events[0].EventType)
	}
	if events[1].EventType != contracts.EventSessionCompleted {
		t.Fatalf("second event = %s, want session_completed", events[1].EventType)
	}
	if events[0].SessionID != events[1].SessionID {
		t.Fatalf("events do not share session id")
	}
	if got := events[0].Payload["harness"]; got != "codex" {
		t.Fatalf("harness = %v, want codex", got)
	}
	prompt, _ := events[0].Payload["redacted_prompt"].(string)
	if strings.Contains(prompt, "super-secret") || !strings.Contains(prompt, "[redacted-secret]") {
		t.Fatalf("prompt was not redacted: %q", prompt)
	}
	tools, ok := events[0].Payload["tools"].([]string)
	if !ok || len(tools) != 2 || tools[0] != "edit" || tools[1] != "read" {
		t.Fatalf("tools = %#v, want sorted edit/read", events[0].Payload["tools"])
	}

	events, err = runner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("second scan events = %d, want deduped 0", len(events))
	}
}

func TestCaptureRunnerSkipsDisabledHarness(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	body, _ := json.Marshal(map[string]any{
		"prompt": "hello",
		"model":  "gpt-5",
	})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	oldMTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, oldMTime, oldMTime); err != nil {
		t.Fatal(err)
	}

	state := &State{}
	state.SetCaptureEnabled("codex", false)
	runner := NewCaptureRunner(CaptureConfig{
		APIToken:   "token",
		WSEndpoint: "ws://example.test/v1/ws",
		Dirs:       []HarnessDir{{Harness: "codex", Path: root}},
		Now:        time.Now,
	}, state, slog.Default())
	events, err := runner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 for disabled harness", len(events))
	}
}

func TestParseHarnessDirs(t *testing.T) {
	root := t.TempDir()
	raw := "codex=" + root
	dirs, err := ParseHarnessDirs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0].Harness != "codex" || dirs[0].Path != root {
		t.Fatalf("dirs = %#v", dirs)
	}
	if _, err := ParseHarnessDirs("bad=" + root); err == nil {
		t.Fatal("expected invalid harness error")
	}
}

func TestCaptureRunnerPublishesFromWALAndKeepsUnacked(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	wal, err := OpenCaptureWAL(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()

	first := CaptureEvent{
		SessionID:  mustUUID(t, "550e8400-e29b-41d4-a716-446655440000"),
		EventType:  contracts.EventPromptSent,
		OccurredAt: now,
		Payload:    map[string]any{"redacted_prompt": "first", "harness": "codex", "model": "gpt-5"},
	}
	second := CaptureEvent{
		SessionID:  mustUUID(t, "550e8400-e29b-41d4-a716-446655440001"),
		EventType:  contracts.EventPromptSent,
		OccurredAt: now.Add(time.Second),
		Payload:    map[string]any{"redacted_prompt": "second", "harness": "codex", "model": "gpt-5"},
	}
	if _, err := wal.AppendBatch(ctx, captureWALEvents([]CaptureEvent{first, second})); err != nil {
		t.Fatal(err)
	}

	publisher := &fakeCapturePublisher{failAfter: 1}
	runner := NewCaptureRunner(CaptureConfig{
		APIToken:   "token",
		WSEndpoint: "ws://example.test/v1/ws",
		Dirs:       []HarnessDir{{Harness: "codex", Path: t.TempDir()}},
		Now:        func() time.Time { return now },
	}, nil, slog.Default())
	runner.publisher = publisher
	runner.scanAndPublishWithWAL(ctx, wal)

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want first only", len(publisher.events))
	}
	unsent, err := wal.Unsent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsent) != 1 || unsent[0].Event.SessionID != second.SessionID {
		t.Fatalf("unsent = %#v, want second only", unsent)
	}
}

type fakeCapturePublisher struct {
	events    []CaptureEvent
	failAfter int
}

func (f *fakeCapturePublisher) Publish(_ context.Context, events []CaptureEvent) error {
	if f.failAfter > 0 && len(f.events) >= f.failAfter {
		return errors.New("boom")
	}
	f.events = append(f.events, events...)
	return nil
}

func mustUUID(t *testing.T, value string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
