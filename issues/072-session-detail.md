---
type: AFK
depends-on:
  - 064-app-shell-titlebar-sidebar-subbar-rail
  - 065-core-component-library
  - 037-sessions-and-scores-detail
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 5 (Session detail: redacted prompt, harness/model/effort/tools, wall time, turn count, score breakdown by signal, attached outcomes, subagent tree collapsed by default). `DESIGN.md` "Trace timeline" + "Outcome grid".

## What to build

The Session detail screen — opened when a user clicks a row in the Me / Team / Sessions list.

1. **Route binding**: `Route.sessionDetail(id)` renders this view; right rail is **dropped** in this screen's two-column grid (per `DESIGN.md` "Layout shell").
2. **Data fetch**: `GET /v1/sessions/:id` + `GET /v1/scores/:session_id` in parallel.
3. **Header**: redacted prompt (mono, wrapping), Harness badge, model name, effort chip (`low|med|high|xhigh|max`), tools chips (`chrome`, `browser_use`, `computer_use`, ...), wall time + turn count (mono), Score chip.
4. **Left column — Trace timeline**: `TraceRow` per event from `session_events`. Subagent rows collapsed by default with "N tool calls · expand". Tool rows show `read_file`, `grep`, `edit_file`, `run_tests`, etc.
5. **Right column — top**: `OutcomeGrid` 2×2 panels (`tests · commits · files · tools used`) derived from outcome events.
6. **Right column — bottom**: Score breakdown — one row per signal in `score.signals` (JSONB) with name, weight, value bar, and contribution to composite. Below: `score.rationale` text. `scorer_version` chip in the header per `ARCHITECTURE.md` §7 row "Scoring bug at scale".
7. **States**: loading skeleton; 404 → "Session not found or you don't have access"; archived session (older than 90 days) → fetch from R2 via `archive_pointers` indirection — show inline notice "Loaded from archive."
8. **Back affordance**: subbar breadcrumb back to the previous list view.

## Acceptance criteria

- [ ] `GET /v1/sessions/:id` + `GET /v1/scores/:session_id` wired; rendered against staging data
- [ ] Trace timeline renders all kinds with the correct chip colors per `DESIGN.md`
- [ ] Subagent rows are collapsed by default and expand on click
- [ ] Outcome grid shows real numbers from outcome events
- [ ] Score breakdown lists every signal in `score.signals`; rationale text visible
- [ ] `scorer_version` chip visible in the header
- [ ] Right rail is **not** rendered (two-column grid only)
- [ ] Archived-session indirection works against an R2-staged fixture
- [ ] Loading / 404 / archive-loaded states implemented

## Blocked by

- Blocked by `issues/064-app-shell-titlebar-sidebar-subbar-rail.md`
- Blocked by `issues/065-core-component-library.md`
- Blocked by `issues/in-progress/037-sessions-and-scores-detail.md`

## User stories addressed

Screen 5 — Session detail.
