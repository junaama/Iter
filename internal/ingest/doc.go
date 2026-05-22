// Package ingest will host the trace-ingestion processor: pulls TraceEvent
// batches off the Redis Stream the WS gateway publishes to, runs them through
// the redaction classifier (internal/redact), persists clean/strippable events
// to session_events, and emits the embedding-worker job.
//
// Intentionally empty at issue 048 — this slice only stamps the §9 Step 3
// repository layout on disk. Implementation lands alongside the Redis Streams
// wiring (issue 050) and the ingest pipeline issues that follow.
package ingest
