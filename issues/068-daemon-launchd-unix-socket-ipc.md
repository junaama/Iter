---
type: AFK
depends-on:
  - 063-xcode-project-design-tokens-theming
---

# AFK — launchd plist, local smoke, and Unix-socket IPC

This issue is AFK-ready on the local Mac. Workers may create the plist, install/uninstall scripts, daemon IPC server/client code, and run a reversible local smoke test. Avoid destructive launchd changes: use idempotent scripts, clean up any test LaunchAgent state before exit, and document any reboot-only verification that cannot be proven inside the worker run.

## Parent PRD

`ARCHITECTURE.md` §4 "Daemon resilience", §6 (IPC over Unix domain socket). §9 Step 8 ("launchd plist for daemon").

## What to build

The daemon process lifecycle on the user's Mac and the Mac app's IPC client.

1. **launchd plist** template for `~/Library/LaunchAgents/dev.iter.IterDaemon.plist` — `RunAtLoad=true`, `KeepAlive=true`, points at the daemon binary installed under `/usr/local/bin/iter-daemon` (or `Iter.app/Contents/MacOS/iter-daemon`).
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

- [ ] launchd plist install/uninstall scripts are idempotent and pass a local `launchctl` smoke where safe; reboot persistence is documented for manual release verification
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
