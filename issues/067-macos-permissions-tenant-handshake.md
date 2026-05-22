---
type: HITL
depends-on:
  - 066-workos-device-code-signin-keychain
---

# HITL — requires real macOS permission prompts

System-level permission prompts (Accessibility + Full Disk Access) only fire on a real grant flow. The tenant join handshake needs an admin to approve from the other side. AFK workers should skip.

## Parent PRD

`ARCHITECTURE.md` §6 Screen 1 (onboarding wizard steps 2–3): grant macOS permissions → tenant confirmation with email-domain match + admin-approval handshake.

## What to build

Onboarding wizard steps 2 and 3.

**Step 2 — Permissions**:
1. Request Accessibility access via `AXIsProcessTrustedWithOptions` with the prompt flag.
2. Request Full Disk Access scoped to harness session directories (Claude Code, Codex, Pi, OpenCode, Gemini CLI session dirs documented in `ARCHITECTURE.md` §1 "Integrations").
3. Polled status indicator per permission; ✓ when granted, action button when not.
4. Cannot proceed to step 3 until both are granted; user can click "I'll set this up later" which routes them to a degraded state (capture disabled, surfaced in menubar).

**Step 3 — Tenant confirmation**:
1. Backend lookup: does the signed-in user's email domain match an existing tenant?
2. If yes: show "Your team's on Iter! Join / Skip". Clicking Join sends a join request that requires admin approval (three-party handshake). User sees "waiting for admin approval" state; backend pushes confirmation via the daemon WS connection once the admin approves.
3. If no: show "Create a new workspace" with the company name pre-filled from the email domain. User confirms or edits.
4. Initial ingest **runs in the background**, not as a wizard blocker — per §6 Screen 1 explicit rule.

**Background ingest start**:
- Wizard completes → app routes to Dashboard — Me.
- Daemon kicks off retro-ingest of the user's harness session files; menubar shows ingest progress.

This slice requires a backend endpoint to (a) check tenant-by-domain, (b) submit join requests, (c) push the admin-approval confirmation. None of those endpoints have dedicated issues yet — flag in the body and add to `ARCHITECTURE.md` §5 endpoint table as part of this work.

## Acceptance criteria

- [ ] Accessibility + Full Disk Access prompts fire on first run; status reflects real grant state
- [ ] Tenant lookup hits the backend and shows Join / Skip / Create per domain match
- [ ] Join flow shows waiting-for-approval state and resolves when the admin approves (real two-machine test)
- [ ] "Set this up later" routes to a degraded state with capture disabled and a menubar warning
- [ ] Wizard does not block on the retro-ingest; user lands on Dashboard — Me immediately
- [ ] Tenant-by-domain, join-request, and admin-approval REST endpoints documented in `ARCHITECTURE.md` §5 and added to a new follow-up issue if not implemented as part of this slice

## Blocked by

- Blocked by `issues/066-workos-device-code-signin-keychain.md`

## User stories addressed

Screen 1 steps 2–3. Required before capture can start.
