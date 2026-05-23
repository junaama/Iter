package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestCaptureWALAppendUnsentAndMarkSent(t *testing.T) {
	ctx := context.Background()
	wal, err := OpenCaptureWAL(ctx, filepath.Join(t.TempDir(), "capture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()

	first := testCaptureWALEvent("first", time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC))
	second := testCaptureWALEvent("second", time.Date(2026, 5, 23, 12, 1, 0, 0, time.UTC))
	firstEntry, err := wal.Append(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	secondEntry, err := wal.Append(ctx, second)
	if err != nil {
		t.Fatal(err)
	}

	unsent, err := wal.Unsent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsent) != 2 {
		t.Fatalf("unsent = %d, want 2", len(unsent))
	}
	if unsent[0].ID != firstEntry.ID || unsent[1].ID != secondEntry.ID {
		t.Fatalf("unsent order = [%d %d], want FIFO [%d %d]", unsent[0].ID, unsent[1].ID, firstEntry.ID, secondEntry.ID)
	}
	if got := unsent[0].Event.Payload["redacted_prompt"]; got != "first" {
		t.Fatalf("first payload = %v, want first", got)
	}

	if err := wal.MarkSent(ctx, firstEntry.ID); err != nil {
		t.Fatal(err)
	}
	unsent, err = wal.Unsent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsent) != 1 || unsent[0].ID != secondEntry.ID {
		t.Fatalf("remaining unsent = %#v, want second only", unsent)
	}
}

func TestCaptureWALAppendIsIdempotentAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "capture.sqlite")
	event := testCaptureWALEvent("same", time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC))

	wal, err := OpenCaptureWAL(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := wal.Append(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	wal, err = OpenCaptureWAL(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()
	second, err := wal.Append(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent append id = %d, want original %d", second.ID, first.ID)
	}
	unsent, err := wal.Unsent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsent) != 1 || unsent[0].ID != first.ID {
		t.Fatalf("unsent after restart = %#v, want single original row", unsent)
	}
}

func TestCaptureWALValidatesRequiredFields(t *testing.T) {
	ctx := context.Background()
	wal, err := OpenCaptureWAL(ctx, filepath.Join(t.TempDir(), "capture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()

	_, err = wal.Append(ctx, CaptureWALEvent{
		EventType:  contracts.EventPromptSent,
		OccurredAt: time.Now(),
		Payload:    map[string]any{"redacted_prompt": "hello"},
	})
	if err == nil {
		t.Fatal("expected missing session id error")
	}
}

func testCaptureWALEvent(prompt string, occurredAt time.Time) CaptureWALEvent {
	return CaptureWALEvent{
		SessionID:  uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		EventType:  contracts.EventPromptSent,
		OccurredAt: occurredAt,
		Payload: map[string]any{
			"harness":         "codex",
			"model":           "gpt-5",
			"redacted_prompt": prompt,
			"source_key":      "session-" + prompt,
		},
	}
}
