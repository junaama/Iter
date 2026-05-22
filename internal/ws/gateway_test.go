package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/iter-dev/iter/pkg/contracts"
)

// stubVerifier returns a Principal or an error based on the token text.
// "good" → success; anything else → error. Tests use this to drive the
// upgrade-auth path without standing up a real JWKS server.
type stubVerifier struct {
	tenantID uuid.UUID
	userID   uuid.UUID
	err      error
}

func newStubVerifier() *stubVerifier {
	return &stubVerifier{
		tenantID: uuid.New(),
		userID:   uuid.New(),
	}
}

func (s *stubVerifier) Verify(_ context.Context, raw string) (contracts.Principal, error) {
	if s.err != nil {
		return contracts.Principal{}, s.err
	}
	if raw != "good" {
		return contracts.Principal{}, errors.New("ws_test: bad token")
	}
	return contracts.Principal{
		UserID:   s.userID,
		TenantID: s.tenantID,
	}, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestServer wires the gateway into an httptest.Server. Caller is
// responsible for closing the server.
func newTestServer(t *testing.T, cfg Config) (*httptest.Server, *Gateway) {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = discardLogger()
	}
	g := NewGateway(cfg)
	srv := httptest.NewServer(http.HandlerFunc(g.ServeHTTP))
	t.Cleanup(srv.Close)
	return srv, g
}

// wsURL converts an http://... httptest URL to its ws:// form.
func wsURL(u string) string {
	return "ws" + strings.TrimPrefix(u, "http")
}

// dial connects with the given Authorization header. If token is empty
// the header is omitted entirely (covers the "missing token" path).
func dial(ctx context.Context, t *testing.T, base, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	opts := &websocket.DialOptions{}
	if token != "" {
		opts.HTTPHeader = http.Header{
			"Authorization": []string{"Bearer " + token},
		}
	}
	return websocket.Dial(ctx, wsURL(base)+"/", opts)
}

func TestGateway_UpgradeWithValidJWT(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, Config{Verifier: newStubVerifier()})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, resp, err := dial(ctx, t, srv.URL, "good")
	if err != nil {
		t.Fatalf("dial: %v (status=%v)", err, resp)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("want 101, got %d", resp.StatusCode)
	}
}

func TestGateway_MissingTokenRejects401(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, Config{Verifier: newStubVerifier()})

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestGateway_InvalidTokenRejects401(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, Config{Verifier: newStubVerifier()})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, resp, err := dial(ctx, t, srv.URL, "bad")
	if err == nil {
		t.Fatal("want dial error on bad token, got nil")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", resp)
	}
}

func TestGateway_NilVerifierReturns503(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, Config{Verifier: nil})

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}

func TestGateway_PingPong(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, Config{Verifier: newStubVerifier()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := dial(ctx, t, srv.URL, "good")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()

	ping := NewPing(time.Now())
	data, _ := json.Marshal(ping)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	typ, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("want text frame, got %v", typ)
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Type != MessageTypePong {
		t.Fatalf("want pong, got %s", env.Type)
	}
	if env.MsgID != ping.MsgID {
		t.Fatalf("want pong msg_id %s, got %s", ping.MsgID, env.MsgID)
	}
}

func TestGateway_UnknownMessageTypeClosesWith1003(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, Config{Verifier: newStubVerifier()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := dial(ctx, t, srv.URL, "good")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// We intentionally do NOT defer conn.Close — the server should
	// close first.

	bogus := map[string]any{
		"type":    "totally.unknown",
		"msg_id":  uuid.New().String(),
		"sent_at": time.Now(),
	}
	data, _ := json.Marshal(bogus)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Drain incoming frames until we see the close (the error ack
	// arrives first, then the close handshake).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		code := websocket.CloseStatus(err)
		if code != websocket.StatusUnsupportedData {
			t.Fatalf("want close 1003, got %v (err=%v)", code, err)
		}
		return
	}
	t.Fatal("server did not close within 3s")
}

func TestGateway_HeartbeatTimeoutCloses1011(t *testing.T) {
	t.Parallel()
	// Drive the heartbeat path with very short intervals so the
	// test completes within seconds. A real connection would never
	// see these values — production uses 30s / 10s.
	srv, _ := newTestServer(t, Config{
		Verifier:          newStubVerifier(),
		HeartbeatInterval: 100 * time.Millisecond,
		HeartbeatTimeout:  200 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := dial(ctx, t, srv.URL, "good")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Don't reply to the server's ping. Read inbound until the
	// server closes us out.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, err := conn.Read(ctx)
		if err == nil {
			// Ignore the server's ping frame — that's
			// exactly the message we're refusing to ack.
			continue
		}
		code := websocket.CloseStatus(err)
		if code != websocket.StatusInternalError {
			t.Fatalf("want close 1011, got %v (err=%v)", code, err)
		}
		return
	}
	t.Fatal("server did not close on heartbeat timeout within 3s")
}

func TestGateway_Backpressure_AcksSurvive(t *testing.T) {
	t.Parallel()
	// Install a slow handler so the writer queue actually fills.
	// We replace the Ingest handler with one that blocks briefly
	// then ACKs.
	cfg := Config{Verifier: newStubVerifier()}
	srv, g := newTestServer(t, cfg)

	// Track how many acks the writer manages to ship.
	var ackedCount atomic.Int64

	// Replace Ingest with a fast handler so we can quickly produce
	// many acks. The point of this test is that the writer
	// channel's backpressure policy preserves acks under load.
	g.Register(MessageTypeIngest, func(_ context.Context, _ contracts.Principal, env Envelope, _ json.RawMessage) Ack {
		return NewAck(env.MsgID, time.Now())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, _, err := dial(ctx, t, srv.URL, "good")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.SetReadLimit(1 << 20)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()

	const N = 5000 // 100k is overkill in the test; 5k exercises the same code path quickly.

	// Reader goroutine drains acks while the writer pumps.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var env Envelope
			if json.Unmarshal(raw, &env) != nil {
				continue
			}
			if env.Type == MessageTypeAck {
				ackedCount.Add(1)
			}
		}
	}()

	// Writer pumps N ingest messages as fast as possible.
	sid := uuid.New()
	for i := 0; i < N; i++ {
		ev := Ingest{
			Envelope: Envelope{
				Type:   MessageTypeIngest,
				MsgID:  uuid.New(),
				SentAt: time.Now(),
			},
			SessionID:  sid,
			EventType:  "tool_call",
			OccurredAt: time.Now(),
		}
		data, _ := json.Marshal(ev)
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("write i=%d: %v", i, err)
		}
	}

	// Poll until the writer flushes every ack OR a generous
	// deadline elapses. Without polling, a 500ms sleep races the
	// write pipeline — under -race the test runs fast enough that
	// 500ms is plenty, but a clean build of the same code finishes
	// before the writer's last batch lands, producing flakes.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ackedCount.Load() >= int64(N) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")
	<-done

	got := ackedCount.Load()
	// The acks-never-drop invariant is the load-bearing assertion:
	// every Ingest produces an Ack, and no Ack drops under
	// backpressure. With the blocking-send fallback in enqueue,
	// every ack MUST arrive — anything below 100% is a real bug,
	// not a timing artifact (the polling loop above already waits
	// up to 5s for in-flight writes to land).
	if got < int64(N) {
		t.Fatalf("ack survival: got %d of %d", got, N)
	}
}

func TestGateway_NoGoroutineLeaks(t *testing.T) {
	// This test connects and disconnects 100 times and verifies
	// that the goroutine count returns to baseline (plus a small
	// fudge factor for httptest's internals).
	srv, g := newTestServer(t, Config{
		Verifier:          newStubVerifier(),
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  2 * time.Second,
	})

	// Warm up: do one connect/disconnect to get httptest and the
	// network stack into steady state before sampling the baseline.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		c, _, err := dial(ctx, t, srv.URL, "good")
		if err != nil {
			t.Fatalf("warmup dial: %v", err)
		}
		_ = c.Close(websocket.StatusNormalClosure, "warmup")
		cancel()
	}
	// Let the close handshake settle.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, _, err := dial(ctx, t, srv.URL, "good")
		if err != nil {
			cancel()
			t.Fatalf("dial %d: %v", i, err)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "cycle")
		cancel()
	}

	// Wait for the server-side close handshake on every
	// connection to land. We poll instead of sleeping a fixed
	// duration so the test stays fast on a fast machine but
	// doesn't flake on a slow CI runner.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if g.ActiveConns() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := g.ActiveConns(); got != 0 {
		t.Fatalf("active conns did not return to 0: got %d", got)
	}

	// Give the goroutine accounting a moment to catch up after
	// the close handshakes settle.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	// Allow a generous slack: TLS, http.Server's accept loop, and
	// other internals fluctuate. The leak signal we care about is
	// a growth proportional to the 100 cycles — anything below
	// 20 extra goroutines is noise.
	if delta := after - baseline; delta > 20 {
		t.Fatalf("goroutine leak: baseline=%d after=%d delta=%d",
			baseline, after, delta)
	}
}

func TestGateway_SubprotocolAuth(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, Config{Verifier: newStubVerifier()})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Browser path: token rides on Sec-WebSocket-Protocol.
	conn, resp, err := websocket.Dial(ctx, wsURL(srv.URL)+"/", &websocket.DialOptions{
		Subprotocols: []string{"iter.bearer.good", "iter.bearer.v1"},
	})
	if err != nil {
		t.Fatalf("dial: %v (resp=%v)", err, resp)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("want 101, got %d", resp.StatusCode)
	}
	if got := conn.Subprotocol(); got != "iter.bearer.v1" {
		t.Fatalf("want subprotocol echo iter.bearer.v1, got %q", got)
	}
}

func TestGateway_GracefulShutdown(t *testing.T) {
	t.Parallel()
	// Verify a parent-context cancellation closes connections with
	// going-away (1001). We hand-roll the server so we can drive the
	// parent context manually.
	g := NewGateway(Config{
		Verifier: newStubVerifier(),
		Logger:   discardLogger(),
	})

	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	var wg sync.WaitGroup
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		g.ServeHTTP(w, r.WithContext(parentCtx))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := dial(ctx, t, srv.URL, "good")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Wait briefly for the goroutines to spin up, then trigger
	// shutdown by cancelling the parent context.
	time.Sleep(50 * time.Millisecond)
	parentCancel()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		code := websocket.CloseStatus(err)
		if code != websocket.StatusGoingAway {
			t.Fatalf("want close 1001, got %v (err=%v)", code, err)
		}
		wg.Wait()
		return
	}
	t.Fatal("server did not close on shutdown within 3s")
}
