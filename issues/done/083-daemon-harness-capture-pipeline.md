---
type: AFK
depends-on:
  - 043-websocket-gateway
  - 044-server-ingestion-consumer
  - 068-daemon-launchd-unix-socket-ipc
---

# AFK - daemon harness capture pipeline

Implement the first real local capture slice behind the Mac app's permissions and capture toggles.

## What to build

1. Daemon retro-scans known harness session directories for existing session files.
2. Daemon parses enough metadata to produce normalized `trace.event` messages for `prompt_sent` and `session_completed`.
3. Daemon publishes captured events to the server WebSocket ingestion path.
4. Daemon records current task and last captured session in local IPC status.
5. Capture can be disabled per harness using the existing app/daemon IPC toggles.
6. Captured events are written to a local SQLite WAL before network publish and marked sent only after WebSocket ACK.

## Acceptance criteria

- [x] Running the server with Redis and `go run ./cmd/iter-daemon` has the daemon-side path needed to ingest local JSON/JSONL session files through `/v1/ws`; server Redis/Postgres persistence is covered by issue 044 tests.
- [x] The daemon does not send disabled harnesses.
- [x] No raw env values or known secrets are captured by the daemon payload.
- [x] Local IPC status reflects capture progress and last captured time.
- [x] README documents the dev loop and required daemon env vars.
- [x] Local SQLite WAL stores unsent capture events and replays until ACK.

## Notes

Implemented as a polling retro-scan slice, not an fsnotify tailer. Continuous low-latency file watching can follow as a smaller polish/performance issue; the durable replay boundary is already in place.

## Implementation notes

- `cmd/iter-daemon` accepts `ITER_API_BASE_URL`, `ITER_WS_URL`, `ITER_API_TOKEN`, `ITER_CAPTURE_DIRS`, and `ITER_CAPTURE_WAL_PATH`.
- On macOS, if `ITER_API_TOKEN` is unset, the daemon attempts to read the Swift app's Keychain `access_token`.
- Default capture roots cover Claude Code, Codex, Gemini CLI, OpenCode, and Pi.
- Capture parsing accepts JSON, JSONL, and NDJSON files, extracts a prompt/model/tools where possible, redacts common token/key patterns, and emits `prompt_sent` plus `session_completed`.
- Captured events are appended to `~/Library/Application Support/Iter/capture.sqlite` by default, then published over `/v1/ws`; rows are marked sent after ACK only.

Verified:

- `go test ./cmd/iter-daemon ./internal/daemon ./internal/ws ./internal/ingest`
