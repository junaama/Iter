package langfuse

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is an async, non-blocking submitter for the Langfuse v3
// /api/public/ingestion endpoint. One goroutine drains a bounded channel,
// batches events on a timer or size threshold, and POSTs them with HTTP
// Basic auth derived from Config. Submit never blocks the caller; a full
// queue logs a warn and drops the event.
//
// Close flushes the in-flight batch and the queue before returning, or
// gives up on ctx.Done(). After Close the client refuses further Submit
// calls (they no-op + drop with a warn so callers don't need to
// nil-check during shutdown).
type Client struct {
	cfg    Config
	logger *slog.Logger
	http   *http.Client

	authHeader string // pre-built "Basic base64(pk:sk)"; never logged
	endpoint   string // "<BaseURL>/api/public/ingestion"

	queue  chan Event
	done   chan struct{} // closed when worker exits after a flush
	stop   chan struct{} // closed by Close to signal worker to exit
	once   sync.Once     // ensures Close runs the worker drain exactly once
	closed atomic_bool   // set inside Close so post-Close Submit can drop
}

// atomic_bool is a tiny sync helper kept inside the package so we don't
// pull in sync/atomic just to flip a single flag. (Go 1.19 sync/atomic
// would do it just as well; this avoids name collisions with hypothetical
// future fields.)
type atomic_bool struct {
	mu sync.RWMutex
	v  bool
}

func (a *atomic_bool) set() {
	a.mu.Lock()
	a.v = true
	a.mu.Unlock()
}

func (a *atomic_bool) get() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.v
}

// NewClient builds a Client and starts its background worker. Returns an
// error only when Config is structurally invalid (missing URL or keys);
// network reachability is NOT probed here — a Langfuse outage at boot
// must not fail boot.
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("langfuse: BaseURL is required")
	}
	if cfg.PublicKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("langfuse: PublicKey and SecretKey are required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultFlushInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}

	creds := cfg.PublicKey + ":" + cfg.SecretKey
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))

	c := &Client{
		cfg:        cfg,
		logger:     logger,
		http:       httpClient,
		authHeader: authHeader,
		endpoint:   strings.TrimRight(cfg.BaseURL, "/") + "/api/public/ingestion",
		queue:      make(chan Event, queueSize),
		done:       make(chan struct{}),
		stop:       make(chan struct{}),
	}

	go c.run()
	return c, nil
}

// Submit enqueues an event for asynchronous delivery. NEVER blocks: when
// the queue is full or the client is already closed the event is dropped
// with a warn-level log. The caller (the LLM router) must not depend on
// emission succeeding.
//
// Safe for a nil receiver — that's the "tracing disabled" case in
// cmd/server and means we should silently no-op.
func (c *Client) Submit(e Event) {
	if c == nil {
		return
	}
	if c.closed.get() {
		c.logger.Warn("langfuse: drop event after Close", "type", e.Type)
		return
	}
	select {
	case c.queue <- e:
		// enqueued
	default:
		c.logger.Warn("langfuse: queue full, dropping event",
			"type", e.Type,
			"queue_size", cap(c.queue),
		)
	}
}

// Close stops the worker, flushes any pending events (best-effort, bounded
// by ctx), and returns. Subsequent Submit calls are dropped.
//
// Idempotent: a second Close is a no-op.
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		c.closed.set()
		close(c.stop)
	})

	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the single worker goroutine. It drains the queue in batches
// triggered by either the flush interval or BatchSize.
func (c *Client) run() {
	defer close(c.done)

	ticker := time.NewTicker(c.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]Event, 0, c.cfg.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.post(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-c.stop:
			// Drain whatever's still in the queue, then exit.
			for {
				select {
				case e := <-c.queue:
					batch = append(batch, e)
					if len(batch) >= c.cfg.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case e := <-c.queue:
			batch = append(batch, e)
			if len(batch) >= c.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// post sends one ingestion batch. All errors are logged and swallowed —
// Langfuse being unreachable must never propagate back to the caller.
func (c *Client) post(events []Event) {
	payload := map[string]any{"batch": events}
	body, err := json.Marshal(payload)
	if err != nil {
		c.logger.Error("langfuse: marshal batch", "err", err, "count", len(events))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		c.logger.Error("langfuse: build request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Warn("langfuse: ingestion POST failed", "err", err, "count", len(events))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Langfuse returns 207 with a per-event error array when some events
	// were rejected. Treat any 2xx as success; log non-2xx with a short
	// excerpt of the response body for debugging.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 207 may still carry per-event errors; surface them at debug.
		if resp.StatusCode == http.StatusMultiStatus {
			snippet, _ := readSnippet(resp.Body, 512)
			if len(snippet) > 0 {
				c.logger.Warn("langfuse: 207 multi-status (some events may have failed)",
					"count", len(events),
					"body", snippet,
				)
			}
		}
		return
	}

	snippet, _ := readSnippet(resp.Body, 512)
	c.logger.Warn("langfuse: ingestion non-2xx",
		"status", resp.StatusCode,
		"count", len(events),
		"body", snippet,
	)
}

// readSnippet reads up to max bytes from r and returns the result as a
// string. Used to surface a small slice of an error-response body in logs
// without unbounded allocation.
func readSnippet(r io.Reader, max int) (string, error) {
	buf := make([]byte, max)
	n, err := io.ReadFull(r, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return string(buf[:n]), nil
	}
	if err != nil {
		return "", fmt.Errorf("read snippet: %w", err)
	}
	return string(buf[:n]), nil
}
