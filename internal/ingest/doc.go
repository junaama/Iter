// Package ingest owns the server-side daemon trace ingestion path.
//
// It has two halves:
//   - a WebSocket handler that durably enqueues parsed trace.event messages
//     into a per-tenant Redis Stream before ACKing the daemon;
//   - a consumer-group worker that re-classifies payloads, persists sessions
//     and events under tenant RLS, and enqueues embedding work.
package ingest
