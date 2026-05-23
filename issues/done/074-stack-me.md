---
type: AFK
depends-on:
  - 064-app-shell-titlebar-sidebar-subbar-rail
  - 065-core-component-library
  - 038-stacks-endpoints
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 6 (Stack — Me: harnesses chips, skills list, doc references, notes textarea, save button, share grants list). §3 "Stacks" (wrapped solution capture, **never** raw configs/env/secrets/MCP credentials; share per team or per individual; never cross-tenant).

## What to build

The user's stack — what harnesses + skills + doc refs + notes they're running — plus the share-grants management UI.

1. **Route binding**: `Route.stack`.
2. **Data fetch**: `GET /v1/stack/me`. If 404 (stack never saved), render a **draft** populated by the daemon's detected current setup per `ARCHITECTURE.md` §6 "Default-state handling" — explicit Save required.
3. **Harness chips**: read-only pills, one per `Harness` short code with its tint. Detected vs explicitly-added distinguished by border style.
4. **Skills list**: skill names + source paths (mono); add/remove rows.
5. **Doc references**: URL or repo path list; add/remove rows. **The UI prevents adding raw configs, env files, or paths matching `.env*`, `*.key`, `*.pem`, `credentials*` — surfaces a "blocked: secrets-shaped path" toast.**
6. **Notes textarea**: sans, 13px, free-form.
7. **Save**: `POST /v1/stack` upserts. Idempotency-Key header included.
8. **Share grants list**: existing share rows from `stack_shares` shown with target (whole team / specific teammate avatar) + revoke button.
9. **Add share**: opens a sheet with two options — "Whole team" (single-click) or "Specific teammate" (avatar picker). `POST /v1/stack/:id/share`.
10. **Per-share file deselection**: when sharing, surface a list of file references in the stack with checkboxes; deselected refs are stripped from the shared payload per §3 Stacks ("On share, the user can deselect any included file reference").
11. **Right rail**: "Shared with me" card listing teammates whose stack has been shared back.

This slice MUST refuse to capture: configs, env vars, secrets, MCP server credentials, raw file contents. Hard rule from §3 + CLAUDE.md "Locked invariants".

## Acceptance criteria

- [x] `GET /v1/stack/me` + draft fallback works on first run
- [x] Harness chips render with the right tints and detected/explicit border styles
- [x] Skills + doc refs + notes editable; Save persists via `POST /v1/stack`
- [x] Secret-shaped path blocked at the input layer with a toast; corresponding test fixture passes
- [x] Add share → "Whole team" creates a `stack_shares` row scoped to the tenant
- [x] Add share → "Specific teammate" creates a per-user share row
- [x] Per-share file deselection works — the POST payload excludes deselected refs
- [x] Revoke share removes the row and rerenders the list
- [x] No raw configs / env / MCP credentials can leave the client (manual review + a regression test)

## Blocked by

- Blocked by `issues/064-app-shell-titlebar-sidebar-subbar-rail.md`
- Blocked by `issues/065-core-component-library.md`
- Blocked by `issues/in-progress/038-stacks-endpoints.md`

## User stories addressed

Screen 6 — Stack / Me.
