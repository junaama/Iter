package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSessionsCursorRoundTripOpaqueTuple(t *testing.T) {
	startedAt := time.Date(2026, 5, 22, 12, 30, 1, 123456789, time.UTC)
	id := uuid.New()

	cursor, err := encodeSessionsCursor(startedAt, id)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	if strings.Contains(cursor, id.String()) || strings.Contains(cursor, "2026-05-22") {
		t.Fatalf("cursor should be opaque, got %q", cursor)
	}

	gotStartedAt, gotID, err := decodeSessionsCursor(cursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if !gotStartedAt.Equal(startedAt) {
		t.Fatalf("started_at round-trip: got %s want %s", gotStartedAt, startedAt)
	}
	if gotID != id {
		t.Fatalf("id round-trip: got %s want %s", gotID, id)
	}
}

func TestSessionsCursorRejectsInvalid(t *testing.T) {
	cases := []string{
		"",
		"not-base64",
		"e30", // {}
	}
	for _, tc := range cases {
		if _, _, err := decodeSessionsCursor(tc); err == nil {
			t.Fatalf("decodeSessionsCursor(%q) expected error", tc)
		}
	}
}
