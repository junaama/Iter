---
type: AFK
depends-on:
  - 063-xcode-project-design-tokens-theming
---

## Parent PRD

`DESIGN.md` "Core components" (Score chip, Status chip, Harness badge, KPI tile, Session list variants, Trace timeline, Outcome grid, Rail cards, Buttons, Segmented control, Search input). `ARCHITECTURE.md` §6.

## What to build

A SwiftUI component library mirroring the locked specs in `DESIGN.md`. Each component is rendered in a SwiftUI `#Preview` with example data so reviewers can compare against the prototype at `design/dashboard-prototype/project/`.

Components (one Swift file each under `mac/IterApp/Components/`):

1. **`Score(value: Int)`** — 30px min, mono, `padding 1px 6px`, radius 4. Thresholds binding: `≥80 → good`, `60–79 → warn`, `<60 → bad`. Bg = `*-soft`, text = `*`.
2. **`StatusChip(status: OutcomeStatus)`** — 6px round dot + mono label. `landed / review / dropped`.
3. **`Harness(id: HarnessID)`** — 6px tinted square (1.5px radius) + mono short code (`cc / cx / oc / gm / pi`). Tints from `DESIGN.md` table.
4. **`KPITile(label: String, value: String, unit: String?, delta: Delta, sparkline: [Double])`** — 4-up grid container `KPIRow(tiles: [KPITile])` with 1px gap dividers, `--radius` bordered. Label 11px t2; value 22px mono weight 500; delta chip up/dn/flat; 22px sparkline.
5. **`SessionTable`** — Linear-dense table. Columns: `Started · Repo·Task · Harness · Dur · Score · Status · Accepted`. Row height 30, hover `--hover`, selected `--accent-soft`. Mono for `when`, `dur`, `accepted`; sans for task; t2 for `repo`.
6. **`SessionCards`** — `cards-grid` auto-fill min 308px. Card spec from `DESIGN.md`: repo + score, task, meta row, 14px tinted bar mini-sparkline using harness tint, footer with status + relative time.
7. **`SessionFeed`** — day-grouped (`Today / Yesterday / Mon …`) with uppercase t3 day headers, 4-column row.
8. **`TraceRow(kind, label, detail)`** — kinds and chip colors per `DESIGN.md` table (`start/end/commit` → good; `prompt/suggest/extract` → accent; `tool` → selected/t2; `subagent` → warn). Row layout `52px · 20px · 1fr`, dashed bottom border. Subagent rows collapsed by default with "N tool calls · expand".
9. **`OutcomeGrid(tests, commits, files, toolsUsed)`** — 2×2 mono panels.
10. **`RailCard(title, count, items)`** — white panel, 11.5px weight-600 t1 header with mono count chip right; item rows have title 12px t1 + meta line mono 10.5px t3 + optional action buttons.
11. **`Button` / `ButtonPrimary`** — 22px tall, sans 11px, 5px radius. Primary = accent bg + white text; hover darkens to `oklch(0.62 0.16 38)`. Inline `KBD(text)` mono 10px hint slot.
12. **`SegmentedControl(items, selection)`** — 26px tall, mono 11px, 1px panel border with internal dividers.
13. **`SearchInput`** — 26px tall, 220px wide, left search icon, right `⌘K` kbd hint.
14. **`Avatar(initials, seed)`** — 4px-rounded square, mono, seeded tint from a fixed palette so the same person is always the same color (matches `styles.css:471-478`).

Visual parity checked against `design/dashboard-prototype/project/styles.css` for any spec not duplicated in `DESIGN.md`. **Do not port React/CSS structure** — match the visual output only.

## Acceptance criteria

- [ ] Each component has a `#Preview` with at least 3 example states (incl. empty / error / interactive where relevant)
- [ ] Score chip thresholds bind to `contracts.py` `CompositeScoreOutput` × 100 (round). The pure mapping function lives in one file.
- [ ] Harness tint table is a single source of truth, extended in lockstep with `contracts.py:Harness` per `DESIGN.md`
- [ ] All numerics, IDs, timestamps render in mono with tabular-nums (per `DESIGN.md` "Reinforced product rules")
- [ ] Buttons hover state matches the locked `oklch(0.62 0.16 38)` darken
- [ ] Subagent `TraceRow` collapses by default; expanding reveals children
- [ ] Suggestion-style rail item primary action button text is **"Copy to clipboard"** (never "Replace")
- [ ] No new colors / type sizes / radii introduced beyond `DESIGN.md`

## Blocked by

- Blocked by `issues/063-xcode-project-design-tokens-theming.md`

## User stories addressed

Foundation for every data screen (070–077). No standalone user story.
