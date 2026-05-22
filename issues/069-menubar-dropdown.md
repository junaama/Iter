---
type: AFK
depends-on:
  - 068-daemon-launchd-unix-socket-ipc
  - 044-server-ingestion-consumer
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 2 (menubar dropdown — always-visible status, current ingest activity, last session captured, Open Dashboard, Share my stack, pause-capture toggle, 5s poll over Unix socket).

## What to build

The menubar item — always-visible Iter status accessible from anywhere on the user's Mac.

1. **Status icon** in the system menubar via `NSStatusItem`. Icon states:
   - running + connected → solid coral mono `i`
   - paused → warn-tinted `i`
   - disconnected → muted/strike-through `i`
2. **Dropdown content**:
   - Header: "Iter — running" / "paused" / "disconnected" with the daemon-status dot
   - "Currently ingesting: `<task>`" or "Idle since `<relative time>`"
   - "Last session captured: `<relative time>`"
   - Divider
   - **Open Dashboard** (focuses the main window or launches it if quit)
   - **Share my stack** (focuses Stack — Me with the share sheet open)
   - **Pause capture** / **Resume capture** toggle
   - Divider
   - **Quit Iter**
3. **Polling**: `DaemonClient` polled every 5s while the dropdown is open; lazy when closed.
4. **Settings link** at the bottom routing to `Route.settings`.
5. **Keyboard**: a global hotkey (configurable later, default off) is not part of this slice.

## Acceptance criteria

- [ ] Menubar icon present on launch; reflects 3 daemon states
- [ ] Dropdown opens on click and renders all rows from §6 Screen 2
- [ ] Polling at 5s while open; no polling when closed (verified via daemon log)
- [ ] Pause / Resume round-trips through the daemon and updates the icon
- [ ] Open Dashboard / Share my stack routes the main window correctly
- [ ] Last-session-captured value reflects the real ingestion consumer (issue 044) state

## Blocked by

- Blocked by `issues/068-daemon-launchd-unix-socket-ipc.md`
- Blocked by `issues/044-server-ingestion-consumer.md`

## User stories addressed

Screen 2 — Menubar dropdown.
