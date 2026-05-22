---
type: AFK
depends-on:
  - 068-daemon-launchd-unix-socket-ipc
  - 035-post-suggest-handler
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 9 (transient suggestion popover: native macOS notification, actions "Copy to clipboard / Dismiss / Suppress this pattern", 8s auto-dismiss, **never injects into a terminal directly — clipboard only**). §5 confidence thresholds + pure decision function `suggestion_action(confidence, refined_prompt)`. `DESIGN.md` "Rail cards" (suggestion item primary action wording **"Copy to clipboard"**).

## What to build

The suggestion notification — the only Iter UI a user sees during their normal flow.

1. **Trigger path**:
   - Harness invokes `iter suggest` (CLI calls `POST /v1/suggest`)
   - Daemon receives a `suggestion.available` push from the cloud over WS (per `ARCHITECTURE.md` §5 "Wire formats")
   - Daemon forwards to the Mac app over the Unix socket (new IPC method `suggestion.available` extending 068)
   - Mac app renders the popover
2. **Decision function**: call the pure `suggestion_action(confidence, refined_prompt)` exactly as defined in `contracts.py`. Confidence thresholds (binding):
   - `< 0.50` → suppress (do not show)
   - `0.50–0.80` → advisory
   - `≥ 0.80` → replace — but **only via clipboard**; never inject
3. **Native notification** via the `UserNotifications` framework:
   - Title: "Iter suggestion"
   - Body: the refined prompt (first ~120 chars, monospace where supported)
   - Actions:
     - **"Copy to clipboard"** (primary — exact wording, not "Replace")
     - **"Dismiss"**
     - **"Suppress this pattern"** (writes a per-tenant suppress rule via a backend endpoint — flag if not yet implemented)
   - Auto-dismiss after 8s
4. **Dangerous-pattern deny-list check** (`rm -rf`, `DROP TABLE`, `git push --force`, etc.) — applied as a final client-side guard even though the server should have filtered. If hit, the popover is suppressed silently and a security event is logged via daemon — matches CLAUDE.md "Locked invariants" and `ARCHITECTURE.md` §7 row "Suggestion contains harmful pattern".
5. **Rationale on hover/click**: clicking the notification opens a tiny floating panel showing `rationale` + `evidence[]` from the suggest response.
6. **No-suggestion paths**: if response carries `no_suggestion_reason`, popover does not fire. Tracked in metrics only.

## Acceptance criteria

- [ ] End-to-end: harness CLI → suggest API → daemon WS push → Unix-socket IPC → notification renders, against staging
- [ ] Decision-function call uses the pure `contracts.py` symbol; thresholds not duplicated anywhere
- [ ] Primary action text reads exactly **"Copy to clipboard"**
- [ ] Auto-dismiss after 8s
- [ ] Clicking Copy puts the **refined_prompt** on the pasteboard; nothing is injected into any terminal
- [ ] Dangerous-pattern deny-list suppresses the popover silently and writes a security event
- [ ] "Suppress this pattern" posts to the (newly-tracked) suppress endpoint and the same prompt doesn't fire a popover next time
- [ ] `no_suggestion_reason` paths do not fire the popover

## Blocked by

- Blocked by `issues/068-daemon-launchd-unix-socket-ipc.md`
- Blocked by `issues/in-progress/035-post-suggest-handler.md`

## User stories addressed

Screen 9 — Suggestion popover. The core feedback loop ("a teammate's learnings show up in your next session as a better prompt").
