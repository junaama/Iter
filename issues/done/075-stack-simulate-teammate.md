---
type: AFK
depends-on:
  - 074-stack-me
  - 038-stacks-endpoints
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 7 (Stack — Simulate teammate: read-only pills showing the teammate's stack composition; "Use stack in directory" opens a new git worktree in a chosen directory as a simulation sandbox; **not** a runtime environment switch).

## What to build

The "try a teammate's stack" view — read-only inspection plus the worktree-picker action.

1. **Route binding**: `Route.stackSimulate(userId)` reached from a teammate's avatar / "Try their stack" link in 073.
2. **Data fetch**: `GET /v1/stack/:user`. Auth + share check enforced server-side; client surfaces a clean error if forbidden.
3. **Read-only pills**: same `Harness` / skills / doc-refs / notes layout as Stack — Me but with all controls disabled and a header "Viewing `<teammate name>`'s stack."
4. **"Use stack in directory" primary button**:
   - Opens `NSOpenPanel` in directory-picker mode
   - On selection, shells out to `git worktree add <chosen-dir>/iter-sim-<userId>-<timestamp> HEAD` against the current Mac app's known repo (read from the menubar's "active session" repo if available; otherwise prompt for the repo path)
   - Surfaces a Finder reveal of the new worktree and copies the worktree path to clipboard
   - Posts a `stack.simulated` event to the daemon for audit purposes
5. **Make explicit in the UI**: this is a **simulation sandbox**, not a runtime environment switch — clarifying line under the button per §6 Screen 7.
6. **No write affordance**: there is no "save this as mine" — explicitly out of scope.

## Acceptance criteria

- [ ] `GET /v1/stack/:user` wired; auth/share check failure renders a clean forbidden state
- [ ] All edit controls disabled in this view
- [ ] Directory picker opens; worktree gets created with the expected name
- [ ] Worktree path copied to clipboard + revealed in Finder
- [ ] Clarifying "simulation sandbox" copy visible
- [ ] `stack.simulated` daemon event posted (verified in daemon logs)
- [ ] No save / copy-to-my-stack affordance present

## Blocked by

- Blocked by `issues/074-stack-me.md`
- Blocked by `issues/in-progress/038-stacks-endpoints.md`

## User stories addressed

Screen 7 — Stack / Simulate teammate.
