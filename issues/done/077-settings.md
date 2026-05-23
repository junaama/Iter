---
type: AFK
depends-on:
  - 064-app-shell-titlebar-sidebar-subbar-rail
  - 065-core-component-library
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 8 (Settings: account, tenant, capture toggles per harness, retention info read-only, redaction rules preview, data export, account deletion, notification toggle). §7 row "User account deleted" (cascade-delete with confirmation). §3 "Pre-ingestion redaction" (three-tier classification preview).

## What to build

The Settings screen.

1. **Route binding**: reached from menubar "Settings" link or sidebar.
2. **Sections** (vertical scroll, each in a bordered `--radius` panel):
   - **Account** — display name (mono), email (mono), sign-in provider, "Sign out" button, "Delete account" (destructive — opens a confirmation modal with `type DELETE to confirm` mono input, see §7).
   - **Tenant** — tenant name, role (admin / member), join date, link to web admin on iter.dev (admin-only).
   - **Capture toggles** — one toggle per harness (`claude-code · codex · pi · opencode · gemini`) with the harness tint + short code. Reads from / writes to the daemon over Unix socket.
   - **Retention info** — read-only summary: "Hot in Postgres for 90 days, then archived to R2. Scored summaries kept indefinitely." Pulled from a single source so it stays in sync with the policy.
   - **Redaction rules preview** — read-only list of the trufflehog detector categories + the three-tier classification (`clean · strippable · dirty`) with a "what stays on device" note.
   - **Data export** — "Export my data" button → posts to a data-export endpoint (flag: no issue yet — add follow-up if not implemented) → polls until ready → opens Finder with the archive.
   - **Notification toggle** — master on/off for the suggestion popover. Default on.
3. **Confirmations**: every destructive action (delete account, revoke all shares) uses the same `ConfirmDestructive(verb, target)` modal with the typed-confirmation pattern.
4. **Inherited defaults**: where a setting is inherited from the tenant default, show the inherited value with its source label per `ARCHITECTURE.md` §6 "Default-state handling" — never implicit.

Backend endpoints not currently tracked as issues but needed here:
- `POST /v1/account/delete`
- `POST /v1/account/export`

Add a follow-up issue if these aren't covered by the in-progress backend work.

## Acceptance criteria

- [ ] All 7 sections present and visible
- [ ] Per-harness capture toggles round-trip through the daemon
- [ ] Retention copy pulled from a single source (no duplicated literal)
- [ ] Redaction rules preview shows the three-tier classification
- [ ] Data export button completes the full flow against staging (or surfaces a clean "endpoint not yet available" notice if the endpoint isn't implemented yet)
- [ ] Delete account requires `type DELETE to confirm` and posts to the delete endpoint
- [ ] Notification toggle suppresses 076's popover when off
- [ ] Inherited-default rows show their source label

## Blocked by

- Blocked by `issues/064-app-shell-titlebar-sidebar-subbar-rail.md`
- Blocked by `issues/065-core-component-library.md`

## User stories addressed

Screen 8 — Settings.
