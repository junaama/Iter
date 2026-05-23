// Package langfuse is a minimal async client for the self-hosted Langfuse
// v3 Ingestion API (POST /api/public/ingestion). It exists so the LLM
// router can emit a "generation" observation per provider call without
// adding the OTel exporter and its dependency surface to the binary.
//
// Design constraints (CLAUDE.md):
//
//   - Non-blocking. Submit enqueues onto a bounded channel and returns
//     immediately. A full channel logs a warn and drops the event; it
//     never blocks the caller.
//   - Fail-safe. Network errors stay inside this package; an LLM call must
//     never fail because Langfuse is unreachable.
//   - No secret leakage. The basic-auth header is constructed inside the
//     HTTP path and never logged. ConfigFromEnv returns ErrDisabled (a
//     sentinel) when the LANGFUSE_* env trio is incomplete so the caller
//     treats it as "tracing off, continue."
//
// The package exposes only what the router and cmd/server need: Config,
// NewClient, Client.Submit, Client.Close, plus the Event constructors in
// event.go. Concrete HTTP shape is hidden behind those constructors so
// the Langfuse wire format can evolve without touching call sites.
package langfuse

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Config describes a Langfuse client. BaseURL is the scheme+host of the
// self-hosted deployment (no trailing slash); PublicKey/SecretKey are the
// per-environment project credentials. HTTPClient and Logger are optional;
// zero values yield sensible defaults.
//
// All fields are read once at NewClient time and not mutated afterwards.
type Config struct {
	// BaseURL is e.g. "https://langfuse-web-dev-6e72.up.railway.app". The
	// client appends "/api/public/ingestion" when posting. A trailing
	// slash is trimmed in NewClient.
	BaseURL string

	// PublicKey is the Langfuse project public key (pk-lf-...). Used as
	// the username in HTTP Basic auth.
	PublicKey string

	// SecretKey is the Langfuse project secret key (sk-lf-...). Used as
	// the password in HTTP Basic auth. Never logged.
	SecretKey string

	// HTTPClient is the transport used for /api/public/ingestion POSTs.
	// Nil → a default *http.Client with a 5s timeout. A custom client is
	// supplied by tests via httptest.NewServer + Client.
	HTTPClient *http.Client

	// Logger receives warn/error lines (channel-full drops, ingestion
	// non-2xx responses). Nil → slog.Default().
	Logger *slog.Logger

	// QueueSize bounds the in-memory event queue. Zero → defaultQueueSize.
	// When the queue is full, Submit drops the event with a warn log.
	QueueSize int

	// FlushInterval is the maximum wall-clock delay between a Submit and
	// the resulting POST. Zero → defaultFlushInterval.
	FlushInterval time.Duration

	// BatchSize is the high-water mark that triggers an early flush
	// before FlushInterval elapses. Zero → defaultBatchSize.
	BatchSize int
}

// Defaults — exported as constants so tests can reference them and ops
// can tune via env vars without re-reading the source.
const (
	defaultQueueSize     = 1024
	defaultFlushInterval = 500 * time.Millisecond
	defaultBatchSize     = 64
	defaultHTTPTimeout   = 5 * time.Second
)

// ErrDisabled is returned by ConfigFromEnv when the LANGFUSE_* env trio is
// incomplete. Callers treat this as a normal "tracing is off" signal and
// continue with a nil *Client; nothing in this package or the LLM router
// must crash on a nil receiver.
var ErrDisabled = errors.New("langfuse: disabled (LANGFUSE_BASE_URL / LANGFUSE_PUBLIC_KEY / LANGFUSE_SECRET_KEY unset)")

// ConfigFromEnv reads the three LANGFUSE_* env vars and returns a Config
// suitable for NewClient. Returns (Config{}, ErrDisabled) when any one is
// empty so the boot path can branch on `errors.Is(err, ErrDisabled)`.
//
// HTTPClient/Logger are left zero — the caller can populate them before
// passing to NewClient (cmd/server attaches the process slog.Logger).
func ConfigFromEnv() (Config, error) {
	baseURL := os.Getenv("LANGFUSE_BASE_URL")
	publicKey := os.Getenv("LANGFUSE_PUBLIC_KEY")
	secretKey := os.Getenv("LANGFUSE_SECRET_KEY")
	if baseURL == "" || publicKey == "" || secretKey == "" {
		return Config{}, ErrDisabled
	}
	return Config{
		BaseURL:   baseURL,
		PublicKey: publicKey,
		SecretKey: secretKey,
	}, nil
}
