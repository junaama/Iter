// Package ws will host the daemon ↔ cloud WebSocket gateway: connection
// lifecycle, message envelope routing (ClientMessage/ServerMessage discriminated
// unions mirrored from contracts.py), per-tenant authorization, replay-ack
// protocol, and idle-timeout management.
//
// Intentionally empty at issue 048 — this slice only stamps the §9 Step 3
// repository layout on disk. The upgrade handler and message dispatcher land
// in issue 043 (WebSocket gateway).
package ws
