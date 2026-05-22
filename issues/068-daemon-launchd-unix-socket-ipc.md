---
type: HITL
depends-on:
  - 063-xcode-project-design-tokens-theming
---

# HITL — requires real launchd registration on a Mac

Installing a launchd plist, verifying daemon autostart, and exercising the Unix socket needs a real Mac with the Go daemon binary built. AFK workers should skip.

## Parent PRD

`ARCHITECTURE.md` §4 "Daemon resilience", §6 (IPC over Unix domain socket). §9 Step 8 ("launchd plist for daemon").

## What to build

The daemon process lifecycle on the user's Mac and the Mac app's IPC client.

1. **launchd plist** at `~/Library/LaunchAgents/dev.iter.IterDaemon.plist` — `RunAtLoad=true`, `KeepAlive=true`, points at the daemon binary installed under `/usr/local/bin/iter-daemon` (or `Iter.app/Contents/MacOS/iter-daemon`).
2. **Install/uninstall scripts** invoked by the Mac app on first run / sign-out, idempotent.
3. **Unix domain socket** at `~/Library/Application Support/Iter/daemon.sock`; daemon listens, Mac app dials.
4. **IPC protocol**: line-delimited JSON, requests carry `id` + `method`, responses carry `id` + `result` or `error`. Methods at this slice:
   - `status` → `{ running: true, last_session_at, captured_today, paused }`
   - `pause` / `resume`
   - `ping` (heartbeat)
   - `version` (for daemon ↔ app version-drift detection per `ARCHITECTURE.md` §7)
5. **Mac app IPC client** (`DaemonClient`): connects on app launch; reconnects with backoff on socket close; surfaces `connected: Bool` to the rest of the app via an observable.
6. **Version-drift check**: on connect, app compares `version()` reply to its own bundle version. Major mismatch → modal "Iter daemon out of date — install update" with a quit-and-relaunch button; daemon refuses to start on major mismatch (matches `ARCHITECTURE.md` §7 row "Mac app / daemon version drift").
7. **Daemon footer dot in sidebar** (from 064): now wired to real daemon state — green pulse when connected + running, warn when paused, bad when not connected.

This slice doesn't implement the daemon's capture pipeline (that's the existing Go daemon work). It implements the lifecycle + IPC surface the Mac app needs.

## Acceptance criteria

- [ ] launchd plist installs on first sign-in and survives reboot (daemon autostarts)
- [ ] Mac app connects to the daemon over Unix socket within 2s of launch
- [ ] `DaemonClient.status` updates the sidebar footer dot in real time
- [ ] `pause` / `resume` from the app reflects in the daemon and in the footer
- [ ] Version-drift modal fires when daemon reports a major-version mismatch
- [ ] Reconnect-with-backoff works when the daemon is killed and restarted by launchd
- [ ] Uninstall script removes the plist and the socket file

## Blocked by

- Blocked by `issues/063-xcode-project-design-tokens-theming.md`

## User stories addressed

Required for menubar (069), suggestion popover (076), and any UI that reads daemon state. No standalone user story.
