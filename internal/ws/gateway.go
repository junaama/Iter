package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/iter-dev/iter/pkg/contracts"
)

// Tuning constants. Values match ARCHITECTURE.md §9 Step 4
// ("heartbeat ping 30s") and the issue 043 brief.
const (
	defaultHeartbeatInterval = 30 * time.Second
	defaultHeartbeatTimeout  = 10 * time.Second

	// writerQueueCapacity bounds the per-connection writer channel.
	// Picked at 64 per the issue brief; large enough that a normal
	// burst (e.g. handler emitting an ack while a heartbeat fires)
	// doesn't drop anything, small enough that a stuck reader can't
	// hoard tens of MB of buffered messages.
	writerQueueCapacity = 64

	// maxReadSize bounds a single inbound JSON frame. ~256 KiB is
	// generous for a SessionEvent payload (typical 21 KB compressed
	// per DECISIONS.md Phase 2) but blocks an attacker from sending
	// a multi-GB single frame.
	maxReadSize = 256 * 1024

	// authSubprotocolPrefix lets browser clients (which cannot set
	// custom request headers from JS) smuggle the bearer token in
	// the Sec-WebSocket-Protocol header — the conventional workaround.
	// Daemon clients use the Authorization header. See DECISIONS.md
	// "WebSocket JWT transport (issue 043)".
	authSubprotocolPrefix = "iter.bearer."

	// negotiatedSubprotocol is what the server echoes back via
	// Sec-WebSocket-Protocol when subprotocol auth is used. It MUST
	// match one of the values the client offered; without an echo
	// the handshake is rejected by the browser.
	negotiatedSubprotocol = "iter.bearer.v1"

	// writeDeadline bounds an individual conn.Write call. A peer with
	// a half-open socket must not be able to pin the writer goroutine
	// indefinitely.
	writeDeadline = 5 * time.Second
)

// tokenVerifier is the subset of *auth.Verifier the gateway needs.
// Pulled out so tests can inject a stub Principal/error without standing
// up a real JWKS server. Matches the shape used by middleware/auth.go.
type tokenVerifier interface {
	Verify(ctx context.Context, raw string) (contracts.Principal, error)
}

// Handler is the per-message contract. Handlers receive the originating
// principal, the parsed envelope (so they can read MsgID without
// re-decoding), and the raw JSON for type-specific decode. They return
// an Ack (success or error variant) which the writer goroutine ships.
//
// Handlers MUST NOT touch the *websocket.Conn directly — the writer
// goroutine is the single owner. Any concurrent write violates the
// coder/websocket invariant and produces a runtime panic.
type Handler func(ctx context.Context, p contracts.Principal, env Envelope, raw json.RawMessage) Ack

// Gateway is the per-process WebSocket entrypoint. Safe for concurrent
// use; the same Gateway value serves every WS upgrade.
type Gateway struct {
	verifier tokenVerifier
	logger   *slog.Logger
	now      func() time.Time

	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration

	mu       sync.RWMutex
	handlers map[MessageType]Handler

	connMu      sync.Mutex
	activeConns int
}

// Config wires the gateway's dependencies. All fields are optional
// except Verifier — a nil verifier rejects every upgrade with
// 503 auth_unavailable, matching the HTTP middleware posture.
type Config struct {
	Verifier tokenVerifier
	Logger   *slog.Logger

	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration

	// Now is the injectable clock — tests use it to drive the
	// heartbeat path without sleeping the wall clock. Zero falls
	// back to time.Now.
	Now func() time.Time
}

// NewGateway builds a Gateway with the default handler registry already
// populated. Ping replies with Pong; Ingest stubs an ACK (issue 044
// replaces with real persistence); reserved discriminators stub-ACK so
// the daemon's wire suite passes against this server today.
func NewGateway(cfg Config) *Gateway {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	hi := cfg.HeartbeatInterval
	if hi <= 0 {
		hi = defaultHeartbeatInterval
	}
	ht := cfg.HeartbeatTimeout
	if ht <= 0 {
		ht = defaultHeartbeatTimeout
	}

	g := &Gateway{
		verifier:          cfg.Verifier,
		logger:            logger,
		now:               now,
		heartbeatInterval: hi,
		heartbeatTimeout:  ht,
		handlers:          map[MessageType]Handler{},
	}
	g.Register(MessageTypePing, pingHandler(now))
	g.Register(MessageTypeIngest, stubIngestHandler(now))
	for _, t := range []MessageType{
		MessageTypeAuthHello,
		MessageTypeTraceSessionStarted,
		MessageTypeTraceSessionCompleted,
		MessageTypeStackUpsert,
		MessageTypeStackShare,
	} {
		g.Register(t, stubAckHandler(now))
	}
	return g
}

// Register installs (or replaces) the handler for t. Safe for concurrent
// use; in practice all Register calls happen at boot before the
// listener is up.
func (g *Gateway) Register(t MessageType, h Handler) {
	g.mu.Lock()
	g.handlers[t] = h
	g.mu.Unlock()
}

// ActiveConns reports the current open-connection count. Used by the
// goroutine-leak test.
func (g *Gateway) ActiveConns() int {
	g.connMu.Lock()
	defer g.connMu.Unlock()
	return g.activeConns
}

// ServeHTTP implements http.Handler. Pipeline:
//
//  1. Extract JWT (Authorization preferred, Sec-WebSocket-Protocol fallback).
//  2. Verify BEFORE the upgrade. Verify failure → 401, no upgrade.
//  3. Upgrade. Connection lifetime owns three goroutines: reader →
//     writer (single owner of conn.Write) ← heartbeat.
//  4. Graceful close with a typed StatusCode on shutdown or error.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.verifier == nil {
		g.logger.LogAttrs(r.Context(), slog.LevelWarn, "ws_auth_unavailable",
			slog.String("path", r.URL.Path))
		http.Error(w, `{"error":"auth_unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	rawToken, subprotoOffered, ok := extractToken(r)
	if !ok {
		g.logger.LogAttrs(r.Context(), slog.LevelWarn, "ws_missing_token",
			slog.String("path", r.URL.Path))
		w.Header().Set("WWW-Authenticate", `Bearer realm="iter"`)
		http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
		return
	}

	principal, err := g.verifier.Verify(r.Context(), rawToken)
	if err != nil {
		g.logger.LogAttrs(r.Context(), slog.LevelWarn, "ws_verify_failed",
			slog.String("path", r.URL.Path),
			slog.String("err", err.Error()))
		w.Header().Set("WWW-Authenticate", `Bearer realm="iter"`)
		http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
		return
	}

	acceptOpts := &websocket.AcceptOptions{}
	if subprotoOffered {
		acceptOpts.Subprotocols = []string{negotiatedSubprotocol}
	}

	conn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		g.logger.LogAttrs(r.Context(), slog.LevelWarn, "ws_accept_failed",
			slog.String("err", err.Error()))
		return
	}
	conn.SetReadLimit(maxReadSize)

	g.connMu.Lock()
	g.activeConns++
	g.connMu.Unlock()
	defer func() {
		g.connMu.Lock()
		g.activeConns--
		g.connMu.Unlock()
	}()

	g.runConnection(r.Context(), conn, principal)
}

// extractToken pulls the bearer JWT from either Authorization (daemon)
// or Sec-WebSocket-Protocol (browser). The second return reports
// whether the subprotocol path was used — the caller echoes the
// negotiated subprotocol on Accept in that case.
func extractToken(r *http.Request) (token string, subproto bool, ok bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(h, prefix) {
			t := strings.TrimSpace(h[len(prefix):])
			if t != "" {
				return t, false, true
			}
		}
		return "", false, false
	}

	if h := r.Header.Get("Sec-WebSocket-Protocol"); h != "" {
		for _, part := range strings.Split(h, ",") {
			p := strings.TrimSpace(part)
			if strings.HasPrefix(p, authSubprotocolPrefix) {
				t := strings.TrimSpace(p[len(authSubprotocolPrefix):])
				if t != "" {
					return t, true, true
				}
			}
		}
	}
	return "", false, false
}

// outboundMessage tags an outbound payload so the writer can apply the
// "acks never drop" rule when the queue is full.
type outboundMessage struct {
	payload any
	isAck   bool
}

// runConnection owns the per-connection lifecycle.
//
// Shutdown sequencing matters in two ways:
//
//  1. coder/websocket cancels the underlying socket when the ctx passed
//     to Read/Write expires (via context.AfterFunc → c.close()). If we
//     fed Read the per-connection context, cancelling it would tear the
//     socket down before Conn.Close had a chance to send the close
//     frame, and the peer would see EOF instead of a typed close code.
//     So Read/Write use context.Background() inside the per-call timeout
//     wrappers, and we drive shutdown by calling Conn.Close() ourselves
//     — that unblocks any pending Read/Write with a Conn.closed signal.
//
//  2. The writer goroutine is the single owner of conn.Write; Conn.Close
//     also writes (the close frame). To avoid concurrent writes we
//     (a) signal goroutines to exit via the shutdown channel,
//     (b) wait for the writer to finish draining,
//     (c) call Conn.Close with the typed status.
func (g *Gateway) runConnection(parentCtx context.Context, conn *websocket.Conn, principal contracts.Principal) {
	out := make(chan outboundMessage, writerQueueCapacity)
	pong := make(chan struct{}, 1)
	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	triggerShutdown := func() {
		shutdownOnce.Do(func() { close(shutdown) })
	}

	var (
		closeMu     sync.Mutex
		closeStatus = websocket.StatusNormalClosure
		closeReason = "bye"
		closeSet    bool
	)
	setClose := func(s websocket.StatusCode, reason string) {
		closeMu.Lock()
		defer closeMu.Unlock()
		if closeSet {
			return
		}
		closeStatus = s
		closeReason = reason
		closeSet = true
	}

	// Watch the parent context for shutdown without coupling reads
	// to it. context.AfterFunc registers a callback that fires on
	// cancellation; we use it to set the typed close reason and
	// trigger the shutdown channel. Returns a stop function so the
	// callback is unregistered when runConnection exits cleanly.
	stopParentWatch := context.AfterFunc(parentCtx, func() {
		setClose(websocket.StatusGoingAway, "server shutdown")
		triggerShutdown()
	})
	defer stopParentWatch()

	var readerWG, writerWG sync.WaitGroup
	readerWG.Add(1)
	writerWG.Add(1)

	// Reader: drives the inbound message loop. Uses Background
	// context so a ctx cancellation does not tear the socket down
	// before we get to send the close frame.
	go func() {
		defer readerWG.Done()
		defer triggerShutdown()
		g.readLoop(context.Background(), conn, principal, out, pong, setClose)
	}()

	// Writer: single owner of conn.Write. Exits when the out
	// channel is closed by the orchestrator (after the reader and
	// heartbeat have stopped producing).
	go func() {
		defer writerWG.Done()
		g.writeLoop(conn, out)
	}()

	// Heartbeat: pings every interval; expects pongs.
	var heartbeatWG sync.WaitGroup
	heartbeatWG.Add(1)
	go func() {
		defer heartbeatWG.Done()
		defer triggerShutdown()
		g.heartbeatLoop(shutdown, out, pong, setClose)
	}()

	// Block until any goroutine triggers shutdown.
	<-shutdown

	// Heartbeat exits first. It's the only senders-to-out other
	// than the reader; once it's done the reader is the lone
	// remaining producer.
	heartbeatWG.Wait()

	closeMu.Lock()
	cs, cr := closeStatus, closeReason
	closeMu.Unlock()

	// Send the close frame BEFORE waiting on reader/writer.
	// coder/websocket serializes Write and Close internally via
	// writeMu, so the in-flight writer goroutine and this Close
	// won't tear the frame stream. Conn.Close also closes the
	// underlying socket on completion, which unblocks the reader's
	// pending conn.Read and (eventually) the writer's pending
	// conn.Write.
	_ = conn.Close(cs, cr)

	// With the socket closed, the reader returns from conn.Read
	// and we can safely close `out`. The writer drains whatever
	// remained queued (writes will fail-fast on a closed socket)
	// and exits.
	readerWG.Wait()
	close(out)
	writerWG.Wait()
}

// readLoop reads JSON frames, dispatches by discriminator, and pushes
// the resulting Ack onto the writer channel. ctx is intentionally a
// long-lived background context — see runConnection for why we don't
// thread the per-connection ctx here.
func (g *Gateway) readLoop(
	ctx context.Context,
	conn *websocket.Conn,
	principal contracts.Principal,
	out chan outboundMessage,
	pong chan<- struct{},
	setClose func(websocket.StatusCode, string),
) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			setClose(websocket.StatusNormalClosure, "peer closed")
			return
		}
		if typ != websocket.MessageText {
			setClose(websocket.StatusUnsupportedData, "binary frames not supported")
			return
		}

		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			ack := NewErrorAck(env.MsgID, "malformed_envelope", g.now())
			g.enqueue(out, outboundMessage{payload: ack, isAck: true})
			setClose(websocket.StatusUnsupportedData, "malformed envelope")
			return
		}

		// Pong routes directly to the heartbeat loop. Pongs do
		// not produce acks.
		if env.Type == MessageTypePong {
			select {
			case pong <- struct{}{}:
			default:
			}
			continue
		}

		g.mu.RLock()
		handler, known := g.handlers[env.Type]
		g.mu.RUnlock()
		if !known {
			g.logger.LogAttrs(ctx, slog.LevelWarn, "ws_unknown_message_type",
				slog.String("type", string(env.Type)),
				slog.String("tenant_id", principal.TenantID.String()))
			ack := NewErrorAck(env.MsgID, "unknown_type", g.now())
			g.enqueue(out, outboundMessage{payload: ack, isAck: true})
			setClose(websocket.StatusUnsupportedData, "unknown message type")
			return
		}

		ack := g.safeInvoke(ctx, handler, principal, env, data)
		g.enqueue(out, outboundMessage{payload: ack, isAck: true})
	}
}

// safeInvoke wraps a handler in panic recovery — a single bad payload
// must not take down the connection.
func (g *Gateway) safeInvoke(
	ctx context.Context,
	handler Handler,
	principal contracts.Principal,
	env Envelope,
	raw []byte,
) (ack Ack) {
	defer func() {
		if rec := recover(); rec != nil {
			g.logger.LogAttrs(ctx, slog.LevelError, "ws_handler_panic",
				slog.Any("panic", rec),
				slog.String("type", string(env.Type)))
			ack = NewErrorAck(env.MsgID, "handler_error", g.now())
		}
	}()
	return handler(ctx, principal, env, raw)
}

// writeLoop is the single owner of conn.Write. It drains the out
// channel until it's closed by the orchestrator; the orchestrator
// closes out only after all senders (reader, heartbeat) have exited
// and after the close frame has been emitted via Conn.Close, so any
// in-flight writes here race only with Conn.Close — coder/websocket
// serializes Write and Close internally via its writeMu.
func (g *Gateway) writeLoop(conn *websocket.Conn, out <-chan outboundMessage) {
	for msg := range out {
		data, err := json.Marshal(msg.payload)
		if err != nil {
			g.logger.LogAttrs(context.Background(), slog.LevelError, "ws_marshal_failed",
				slog.String("err", err.Error()))
			continue
		}
		// Use a background-derived ctx so a parent shutdown
		// doesn't cancel the underlying socket mid-frame (per
		// the AfterFunc race documented on runConnection). A
		// half-open socket is bounded by the write deadline.
		wctx, cancel := context.WithTimeout(context.Background(), writeDeadline)
		err = conn.Write(wctx, websocket.MessageText, data)
		cancel()
		if err != nil {
			g.logger.LogAttrs(context.Background(), slog.LevelWarn, "ws_write_failed",
				slog.String("err", err.Error()))
			// Drain the rest of out without writing so the
			// orchestrator's close(out) can complete; don't
			// return early or we'd leak the channel.
			for range out {
			}
			return
		}
	}
}

// heartbeatLoop pings every heartbeatInterval and expects a pong within
// heartbeatTimeout. Miss → close 1011 / "heartbeat timeout". Exits when
// the shutdown channel closes.
func (g *Gateway) heartbeatLoop(
	shutdown <-chan struct{},
	out chan outboundMessage,
	pong <-chan struct{},
	setClose func(websocket.StatusCode, string),
) {
	ticker := time.NewTicker(g.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-shutdown:
			return
		case <-ticker.C:
			ping := NewPing(g.now())
			g.enqueue(out, outboundMessage{payload: ping, isAck: false})

			timer := time.NewTimer(g.heartbeatTimeout)
			select {
			case <-shutdown:
				timer.Stop()
				return
			case <-pong:
				timer.Stop()
			case <-timer.C:
				setClose(websocket.StatusInternalError, "heartbeat timeout")
				return
			}
		}
	}
}

// enqueue applies backpressure. Per the CLAUDE.md / issue 043 contract:
//
//   - Acks NEVER drop. If the queue is full when an ack arrives we
//     first try to evict the oldest non-ack to make room (drop the
//     stale heartbeat ping in favor of the in-flight ack); if every
//     queued entry is an ack we BLOCK the caller until the writer
//     drains a slot. Blocking is acceptable because the only caller
//     producing acks is the reader goroutine, which is already
//     serialized 1-message-at-a-time; backpressuring the reader
//     naturally backpressures the daemon's WAL replay (the daemon
//     will wait before sending the next frame).
//
//   - Non-ack messages (heartbeat pings, future server-pushed
//     suggestions) DROP on full queue with a `ws_backpressure_drop`
//     log. Blocking on a non-ack would let a stuck peer pin a
//     reader-side caller; better to drop and let the heartbeat timer
//     catch a wedged connection.
func (g *Gateway) enqueue(out chan outboundMessage, msg outboundMessage) {
	// Fast path: non-blocking send.
	select {
	case out <- msg:
		return
	default:
	}

	if !msg.isAck {
		g.logger.LogAttrs(context.Background(), slog.LevelWarn, "ws_backpressure_drop",
			slog.String("kind", "non-ack"))
		return
	}

	// Ack path: try to evict the oldest non-ack so we don't have
	// to block. Drain entries into a temporary slice; the first
	// non-ack we hit gets dropped.
	preserved := make([]outboundMessage, 0, writerQueueCapacity)
	evicted := false
	for i := 0; i < writerQueueCapacity; i++ {
		select {
		case existing := <-out:
			if !existing.isAck {
				g.logger.LogAttrs(context.Background(), slog.LevelWarn, "ws_backpressure_drop",
					slog.String("kind", "non-ack-evicted"))
				evicted = true
			} else {
				preserved = append(preserved, existing)
			}
		default:
			// Queue is empty — nothing else to drain.
			i = writerQueueCapacity
		}
		if evicted {
			break
		}
	}

	// Re-queue preserved acks in FIFO order. We just freed at
	// least len(preserved)+1 slots, so non-blocking sends should
	// succeed; on a race with the writer, fall back to blocking
	// to honor the never-drop invariant.
	for _, p := range preserved {
		select {
		case out <- p:
		default:
			out <- p
		}
	}

	// Send the new ack. If we evicted a non-ack we have a slot;
	// otherwise we block on the channel to apply backpressure.
	out <- msg
}

// pingHandler replies to an inbound Ping with a Pong on the same
// msg_id. The contracts.py protocol uses Pong (not Ack) as the reply,
// so we emit a Pong-shaped value via the Handler return — the writer
// ships whatever payload we return; only the Type discriminator
// matters on the wire.
func pingHandler(now func() time.Time) Handler {
	return func(_ context.Context, _ contracts.Principal, env Envelope, _ json.RawMessage) Ack {
		return Ack{
			Envelope: Envelope{
				Type:   MessageTypePong,
				MsgID:  env.MsgID,
				SentAt: now(),
			},
			AckMsgID: env.MsgID,
			Status:   "ok",
		}
	}
}

// stubAckHandler is the no-op handler used for reserved message types
// whose owning slice hasn't landed yet. ACKs without doing work so the
// daemon's wire test suite passes against this server today.
func stubAckHandler(now func() time.Time) Handler {
	return func(_ context.Context, _ contracts.Principal, env Envelope, _ json.RawMessage) Ack {
		return NewAck(env.MsgID, now())
	}
}

// stubIngestHandler is the issue 043 placeholder for the daemon's
// primary message type. Issue 044 replaces this with the real
// ingestion-consumer write path. Today we validate the payload shape
// and ack without persisting.
func stubIngestHandler(now func() time.Time) Handler {
	return func(_ context.Context, _ contracts.Principal, env Envelope, raw json.RawMessage) Ack {
		var ev Ingest
		if err := json.Unmarshal(raw, &ev); err != nil {
			return NewErrorAck(env.MsgID, "malformed_ingest", now())
		}
		var zero [16]byte
		if ev.SessionID == zero {
			return NewErrorAck(env.MsgID, "missing_session_id", now())
		}
		return NewAck(env.MsgID, now())
	}
}
