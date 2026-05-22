package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iter-dev/iter/internal/api/middleware"
)

// captureCtxID runs RequestID with a handler that records the id seen on
// the request context plus the response X-Request-ID header.
func captureCtxID(t *testing.T, inbound string) (ctxID string, headerID string, status int) {
	t.Helper()

	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := middleware.RequestIDFromContext(r.Context())
		ctxID = id
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if inbound != "" {
		req.Header.Set(middleware.HeaderRequestID, inbound)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return ctxID, rec.Header().Get(middleware.HeaderRequestID), rec.Code
}

func TestRequestID_InboundPreserved(t *testing.T) {
	t.Parallel()

	in := "client-trace-123"
	ctxID, hdrID, _ := captureCtxID(t, in)
	if ctxID != in {
		t.Fatalf("ctx id: got %q want %q", ctxID, in)
	}
	if hdrID != in {
		t.Fatalf("hdr id: got %q want %q", hdrID, in)
	}
}

func TestRequestID_MissingMinted(t *testing.T) {
	t.Parallel()

	ctxID, hdrID, _ := captureCtxID(t, "")
	if ctxID == "" {
		t.Fatal("ctx id is empty; expected minted ULID")
	}
	if ctxID != hdrID {
		t.Fatalf("ctx %q != hdr %q", ctxID, hdrID)
	}
	if len(ctxID) != 26 { // standard ULID length
		t.Fatalf("minted id len: got %d (%q) want 26", len(ctxID), ctxID)
	}
}

func TestRequestID_MalformedReplaced(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, in string
	}{
		{"too_long", strings.Repeat("a", 65)},
		{"whitespace", "has space"},
		{"tab", "has\ttab"},
		{"non_ascii", "id-ÿ"},
		{"control", "id-\x01"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctxID, hdrID, _ := captureCtxID(t, tc.in)
			if ctxID == tc.in {
				t.Fatalf("inbound %q should have been replaced", tc.in)
			}
			if ctxID == "" || hdrID == "" {
				t.Fatal("replacement id empty")
			}
			if ctxID != hdrID {
				t.Fatalf("ctx %q != hdr %q", ctxID, hdrID)
			}
			if len(ctxID) != 26 {
				t.Fatalf("replacement not ULID-length: got %d (%q)", len(ctxID), ctxID)
			}
		})
	}
}

func TestRequestID_ContextHelpers(t *testing.T) {
	t.Parallel()

	if _, ok := middleware.RequestIDFromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context()); ok {
		t.Fatal("bare context should not carry a request id")
	}

	ctx := middleware.WithRequestID(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "abc")
	got, ok := middleware.RequestIDFromContext(ctx)
	if !ok || got != "abc" {
		t.Fatalf("WithRequestID round-trip: got (%q, %v)", got, ok)
	}
}
