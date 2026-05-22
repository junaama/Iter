---
type: AFK
depends-on:
  - 063-xcode-project-design-tokens-theming
---

## Parent PRD

`ARCHITECTURE.md` §6 (UI/UX shell). `DESIGN.md` "Layout shell" (ASCII diagram) + "Reinforced product rules".

## What to build

The Mac window chrome and 3-column layout shell every data screen lives inside. No data wiring yet — stub views in each region.

1. **Titlebar (38px)**: traffic lights left, centered mono title `iter — Workspace`, right cluster = daemon-status pill (stub: green dot + "daemon idle") · **⌘K search input** (26px tall, 220px wide, mono `⌘K` kbd hint, focusable, no command palette behind it yet — opens an empty popover) · theme toggle button.
2. **Sidebar (224px, `--sidebar` bg)**:
   - Workspace switcher row
   - Nav: `Me · Team · Sessions · Stack` (active state = `--selected` bg + accent icon + weight 500)
   - "Active stack" pills section (stub)
   - "Recent sessions" list (stub)
   - Daemon footer with green pulse dot (stub)
3. **Subbar (42px)**: breadcrumb left, tabs middle, segmented control right (`Table · Cards · Feed`) — the segmented control's selected value is stored in a `LayoutVariantStore` even though no screen consumes it yet.
4. **Main pane**: empty, padding `18px 22px 28px`.
5. **Right rail (296px on Me/Team, 320px on Session detail)**: `--rail` bg, dropped when the current route is `session-detail` (route is a stub enum).
6. **Window shadow** + stage backdrop tokens from `DESIGN.md` applied.
7. **Routing**: lightweight enum-based router (`Route.me / .team / .sessions / .stack / .sessionDetail(id)`); sidebar nav switches the route; rail visibility follows.
8. **⌘K shortcut** focuses the titlebar search input from anywhere in the app.

## Acceptance criteria

- [ ] Layout matches the `DESIGN.md` ASCII diagram dimensions exactly (sidebar 224, subbar 42, titlebar 38, rail 296/320)
- [ ] `⌘K` focuses the search input from any subview
- [ ] Theme toggle in titlebar swaps light/dark
- [ ] Sidebar nav switches main pane region (all 4 routes render stubs)
- [ ] Rail is hidden when route = `sessionDetail`
- [ ] Segmented `Table · Cards · Feed` control persists selection in `LayoutVariantStore`
- [ ] No emoji, no gradients, mono everywhere `DESIGN.md` requires it (title, kbd hints, daemon-status pill text)

## Blocked by

- Blocked by `issues/063-xcode-project-design-tokens-theming.md`

## User stories addressed

Foundation for every data screen (070–077). No standalone user story.
