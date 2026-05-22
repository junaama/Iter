package ws

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MessageType is the canonical wire-discriminator value for every WebSocket
// envelope. Mirrors the Literal["..."] values on _WSBase subclasses in
// contracts.py. The router keys handler registrations by these constants.
//
// Only Ping/Pong/Ack/Ingest are exercised by issue 043; the remaining tokens
// are reserved so the router can route them to no-op handlers (or future
// real handlers) without changing the envelope shape.
type MessageType string

const (
	// Ping/Pong: server-initiated heartbeat. The daemon replies to a
	// server Ping with a client Pong carrying the same msg_id (per the
	// "ack-every-message" invariant). We use application-level
	// Ping/Pong frames rather than the wire-level WebSocket ping/pong
	// because (a) the contracts.py protocol is symmetric across
	// directions and (b) it lets the heartbeat path exercise the same
	// router-and-writer code as every other message.
	MessageTypePing MessageType = "ping"
	MessageTypePong MessageType = "pong"

	// Ack: every ClientMessage produces a server Ack referencing the
	// originating msg_id. The status field is "ok" on success and a
	// short stable error code on failure ("unknown_type",
	// "handler_error", "auth_violation", ...).
	MessageTypeAck MessageType = "ack"

	// Ingest: the daemon's primary message type, wrapping a single
	// SessionEvent. Issue 044 fills the actual ingestion logic; the
	// 043 stub ACKs without persisting.
	MessageTypeIngest MessageType = "trace.event"

	// Reserved discriminators — recognised by the router but stubbed.
	// Full handlers land in their own slices.
	MessageTypeAuthHello             MessageType = "auth.hello"
	MessageTypeTraceSessionStarted   MessageType = "trace.session_started"
	MessageTypeTraceSessionCompleted MessageType = "trace.session_completed"
	MessageTypeStackUpsert           MessageType = "stack.upsert"
	MessageTypeStackShare            MessageType = "stack.share"
	MessageTypeSuggestionAvailable   MessageType = "suggestion.available"
	MessageTypeSuggestionPreempt     MessageType = "suggestion.preempt"
	MessageTypeServerError           MessageType = "error"
)

// Envelope is the minimal shape every message shares — discriminator +
// msg_id + sent_at. The router parses inbound JSON into an Envelope first
// to dispatch on Type, then re-decodes the same bytes into the concrete
// message type via the registered handler.
//
// Mirrors contracts.py:_WSBase. Field tags are JSON-only — the WS wire
// format is JSON, not pgx, so no `db:` tags here.
type Envelope struct {
	Type   MessageType `json:"type"`
	MsgID  uuid.UUID   `json:"msg_id"`
	SentAt time.Time   `json:"sent_at"`
}

// Ping is sent by the server every heartbeatInterval. The daemon must
// reply with a Pong carrying the matching MsgID within heartbeatTimeout.
type Ping struct {
	Envelope
}

// Pong is the client's reply to a server Ping. The server's heartbeat
// loop reads pongs off a side channel and resets its timeout window.
type Pong struct {
	Envelope
}

// Ack is the server's per-message receipt. Status is "ok" on success;
// any other value is a short stable error code (no stack traces, no SQL
// strings — those land in the structured log only).
type Ack struct {
	Envelope
	AckMsgID uuid.UUID `json:"ack_msg_id"`
	Status   string    `json:"status"`
	// Code is an optional short stable error tag — populated only when
	// Status != "ok". Examples: "unknown_type", "handler_error",
	// "auth_violation". Empty on success.
	Code string `json:"code,omitempty"`
}

// Ingest is the daemon's primary message: a single SessionEvent wrapped
// in the WS envelope. Issue 044 will read SessionID + EventType +
// Payload off this struct and write to session_events.
//
// Field shape mirrors contracts.py TraceEvent — keep changes in lockstep
// with the Python file until the Go server fully replaces it.
type Ingest struct {
	Envelope
	SessionID  uuid.UUID       `json:"session_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// ServerError is a server-pushed error frame for fatal conditions that
// don't map cleanly to an Ack (e.g. tenant_isolation_violation,
// protocol_error). The connection is closed immediately after sending.
type ServerError struct {
	Envelope
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// newEnvelope stamps a fresh msg_id + sent_at using the supplied clock.
// Centralising this here keeps the router from importing uuid/time at the
// per-handler call site.
func newEnvelope(t MessageType, now time.Time) Envelope {
	return Envelope{
		Type:   t,
		MsgID:  uuid.New(),
		SentAt: now,
	}
}

// NewAck builds an Ack referencing inMsgID with status="ok". Use for the
// happy path inside a handler.
func NewAck(inMsgID uuid.UUID, now time.Time) Ack {
	return Ack{
		Envelope: newEnvelope(MessageTypeAck, now),
		AckMsgID: inMsgID,
		Status:   "ok",
	}
}

// NewErrorAck builds an Ack with status="error" + a stable error code.
// The code is intended for log/metric pivoting; never include free-form
// error text here (security posture — see DECISIONS.md).
func NewErrorAck(inMsgID uuid.UUID, code string, now time.Time) Ack {
	return Ack{
		Envelope: newEnvelope(MessageTypeAck, now),
		AckMsgID: inMsgID,
		Status:   "error",
		Code:     code,
	}
}

// NewPing builds a server-initiated Ping. The heartbeat loop creates
// these on a timer; the writer goroutine ships them on the wire.
func NewPing(now time.Time) Ping {
	return Ping{Envelope: newEnvelope(MessageTypePing, now)}
}

// NewPong builds a client-shaped Pong reply for an inbound Ping.
// Exported because the test harness uses it to drive the heartbeat path.
func NewPong(replyTo uuid.UUID, now time.Time) Pong {
	// Pong carries the *originating* Ping's msg_id so the server's
	// heartbeat loop can match it. Per contracts.py the ack-msg_id
	// lives on Ack, not Pong, so we reuse the envelope's MsgID slot
	// for the correlation id and stamp SentAt with the reply time.
	return Pong{Envelope: Envelope{
		Type:   MessageTypePong,
		MsgID:  replyTo,
		SentAt: now,
	}}
}
