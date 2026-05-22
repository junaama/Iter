package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/api/middleware"
	"github.com/iter-dev/iter/pkg/contracts"
)

// newJSONLogger returns a slog.Logger that writes JSON lines into buf at
// debug level so we can capture even 2xx requests.
func newJSONLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// runLogger composes RequestID → Logger so the captured line has
// request_id populated (mirrors production order).
func runLogger(handler http.Handler, log *slog.Logger) (*httptest.ResponseRecorder, []map[string]any, error) {
	chain := middleware.Chain(middleware.RequestID, middleware.Logger(log))
	wrapped := chain(handler)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec, nil, nil
}

func parseLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("unmarshal log line %q: %v", string(line), err)
		}
		out = append(out, m)
	}
	return out
}

func TestLogger_OneLinePerRequest_RequiredFields(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := newJSONLogger(buf)

	rec, _, _ := runLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}), log)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}

	lines := parseLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 log line, got %d: %s", len(lines), buf.String())
	}
	line := lines[0]
	for _, k := range []string{"method", "path", "status", "bytes", "duration_ms", "request_id"} {
		if _, ok := line[k]; !ok {
			t.Errorf("missing key %q in %v", k, line)
		}
	}
	if line["method"] != "GET" || line["path"] != "/x" {
		t.Errorf("method/path: %v", line)
	}
	if line["status"].(float64) != float64(http.StatusOK) {
		t.Errorf("status: %v", line["status"])
	}
	if line["bytes"].(float64) != 5 {
		t.Errorf("bytes: got %v want 5", line["bytes"])
	}
	if line["msg"] != "http_request" {
		t.Errorf("msg: %v", line["msg"])
	}
	// 2xx => debug level
	if line["level"] != "DEBUG" {
		t.Errorf("level: got %v want DEBUG", line["level"])
	}
}

func TestLogger_LevelByStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		level  string
	}{
		{"server_error_5xx_error", 500, "ERROR"},
		{"client_error_4xx_info", 404, "INFO"},
		{"success_2xx_debug", 200, "DEBUG"},
		{"redirect_3xx_debug", 302, "DEBUG"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := &bytes.Buffer{}
			log := newJSONLogger(buf)
			_, _, _ = runLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}), log)
			lines := parseLines(t, buf)
			if len(lines) != 1 {
				t.Fatalf("want 1 line, got %d", len(lines))
			}
			if lines[0]["level"] != tc.level {
				t.Fatalf("level: got %v want %v", lines[0]["level"], tc.level)
			}
		})
	}
}

// TestLogger_ImplicitOKStatus ensures handlers that Write without
// WriteHeader are logged as 200 (matching net/http semantics).
func TestLogger_ImplicitOKStatus(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := newJSONLogger(buf)
	_, _, _ = runLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x"))
	}), log)
	lines := parseLines(t, buf)
	if lines[0]["status"].(float64) != 200 {
		t.Fatalf("implicit status: got %v", lines[0]["status"])
	}
}

func TestLogger_DoubleWriteHeaderIgnored(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := newJSONLogger(buf)
	_, _, _ = runLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.WriteHeader(http.StatusInternalServerError) // ignored
		_, _ = w.Write([]byte("ok"))
	}), log)
	lines := parseLines(t, buf)
	if lines[0]["status"].(float64) != float64(http.StatusTeapot) {
		t.Fatalf("status: got %v want %d", lines[0]["status"], http.StatusTeapot)
	}
}

func TestLogger_IncludesPrincipal(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := newJSONLogger(buf)

	tenant := uuid.New()
	user := uuid.New()
	p := contracts.Principal{UserID: user, TenantID: tenant}

	// Manually compose: RequestID → injects principal → Logger
	injectPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), p)))
		})
	}
	chain := middleware.Chain(middleware.RequestID, injectPrincipal, middleware.Logger(log))
	h := chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	lines := parseLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	if lines[0]["tenant_id"] != tenant.String() {
		t.Errorf("tenant_id: got %v want %v", lines[0]["tenant_id"], tenant)
	}
	if lines[0]["user_id"] != user.String() {
		t.Errorf("user_id: got %v want %v", lines[0]["user_id"], user)
	}
}

// TestLogger_NoPrincipalNoTenantField ensures tenant_id/user_id are
// omitted when no principal is attached.
func TestLogger_NoPrincipalNoTenantField(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := newJSONLogger(buf)
	_, _, _ = runLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), log)
	lines := parseLines(t, buf)
	if _, ok := lines[0]["tenant_id"]; ok {
		t.Error("tenant_id should be absent when no principal")
	}
	if _, ok := lines[0]["user_id"]; ok {
		t.Error("user_id should be absent when no principal")
	}
}
