// Package ws is the daemon ↔ cloud WebSocket gateway: connection
// lifecycle, message envelope routing (ClientMessage/ServerMessage
// discriminated unions mirrored from contracts.py), per-tenant
// authorization, ack-every-message protocol, heartbeat (30s ping, 10s
// timeout), and backpressure (writer chan capacity 64, acks never drop).
//
// The exported entry point is Gateway, constructed once at boot via
// NewGateway and registered on GET /v1/ws by the api router. ServeHTTP
// authenticates the bearer JWT BEFORE upgrading the WebSocket — auth
// failures return 401 with no resource expenditure on the socket. The
// JWT may arrive via either the Authorization header (daemon path) or
// the Sec-WebSocket-Protocol header (browser path); see DECISIONS.md
// "WebSocket JWT transport (issue 043)".
//
// Handler registration: external packages call Gateway.Register at boot
// to install per-MessageType handlers. NewGateway pre-registers Ping
// (replies with Pong) and Ingest (stub — issue 044 fills the real
// ingestion-consumer write path).
package ws
