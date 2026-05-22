package middleware

import (
	"testing"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Internal tests for the small pure helpers in ratelimit.go. These
// would be tedious to cover end-to-end via miniredis (the script
// always returns int64 + string under the live client, so the
// alternative branches in parseScriptResult need direct exercise).

func TestParseScriptResult_HappyPath(t *testing.T) {
	t.Parallel()
	allowed, oldest, err := parseScriptResult([]any{int64(1), "1700000000000"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if allowed != 1 {
		t.Fatalf("allowed: %d", allowed)
	}
	if oldest != 1700000000000 {
		t.Fatalf("oldest: %d", oldest)
	}
}

func TestParseScriptResult_AllowedAsString(t *testing.T) {
	t.Parallel()
	allowed, _, err := parseScriptResult([]any{"0", "0"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if allowed != 0 {
		t.Fatalf("allowed: %d", allowed)
	}
}

func TestParseScriptResult_OldestAsInt(t *testing.T) {
	t.Parallel()
	_, oldest, err := parseScriptResult([]any{int64(1), int64(42)})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if oldest != 42 {
		t.Fatalf("oldest: %d", oldest)
	}
}

func TestParseScriptResult_BadShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   any
	}{
		{"not-array", "garbage"},
		{"short-array", []any{int64(1)}},
		{"bad-allowed-string", []any{"not-a-number", "0"}},
		{"unexpected-allowed-type", []any{3.14, "0"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := parseScriptResult(c.in); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestParseScriptResult_OldestFallbacks(t *testing.T) {
	t.Parallel()
	// Unparseable string oldest → treated as no-data (0), no error.
	allowed, oldest, err := parseScriptResult([]any{int64(1), "not-a-number"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if allowed != 1 || oldest != 0 {
		t.Fatalf("allowed=%d oldest=%d", allowed, oldest)
	}
	// Unexpected oldest type → treated as no-data.
	allowed, oldest, err = parseScriptResult([]any{int64(1), 3.14})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if allowed != 1 || oldest != 0 {
		t.Fatalf("allowed=%d oldest=%d", allowed, oldest)
	}
}

func TestComputeRetryAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		now      int64
		oldest   int64
		window   int64
		expected int
	}{
		{"oldest-zero-returns-full-window", 1_000_000, 0, 60_000, 60},
		{"oldest-negative-returns-full-window", 1_000_000, -10, 60_000, 60},
		{"oldest-equals-now-returns-window", 1_000_000, 1_000_000, 60_000, 60},
		{"oldest-15s-old-returns-45s", 1_015_000, 1_000_000, 60_000, 45},
		{"oldest-rolled-off-returns-1", 2_000_000, 1_000_000, 60_000, 1},
		{"sub-second-remainder-rounds-up", 1_000_500, 1_000_000, 60_000, 60},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := computeRetryAfter(c.now, c.oldest, c.window)
			if got != c.expected {
				t.Fatalf("got %d want %d", got, c.expected)
			}
		})
	}
}

func TestLimitForTokenType(t *testing.T) {
	t.Parallel()
	cfg := rateLimitOptions{cliLimit: 100, daemonLimit: 600, fallbackLimit: 50}
	cases := []struct {
		tokenType string
		want      int
	}{
		{"cli", 100},
		{"daemon", 600},
		{"", 50},
		{"ci", 50},
		{"unknown", 50},
	}
	for _, c := range cases {
		if got := limitForTokenType(c.tokenType, cfg); got != c.want {
			t.Fatalf("%q: got %d want %d", c.tokenType, got, c.want)
		}
	}
}

func TestBuildRateLimitKey(t *testing.T) {
	t.Parallel()
	// Token id present → used directly.
	got := buildRateLimitKey("jti-abc", "user-id")
	if got != "ratelimit:jti-abc" {
		t.Fatalf("got %q", got)
	}
	// Empty token id → user-derived fallback.
	got = buildRateLimitKey("", "user-1")
	if len(got) == 0 || got[:len("ratelimit:u-")] != "ratelimit:u-" {
		t.Fatalf("got %q", got)
	}
	// Same user → stable key (hash determinism).
	g1 := buildRateLimitKey("", "user-1")
	g2 := buildRateLimitKey("", "user-1")
	if g1 != g2 {
		t.Fatalf("fallback unstable: %q vs %q", g1, g2)
	}
	// Different users → different keys.
	if buildRateLimitKey("", "user-1") == buildRateLimitKey("", "user-2") {
		t.Fatalf("user-derived keys collide")
	}
}

func TestLogRateLimit_NilLoggerNoPanic(t *testing.T) {
	t.Parallel()
	// Both event helpers must early-return on nil logger.
	logRateLimitUnavailable(nil, nil, "x", nil)
	logRateLimitExceeded(nil, nil, contracts.Principal{}, 1)
}
