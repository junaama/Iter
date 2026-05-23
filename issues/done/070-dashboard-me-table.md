---
type: AFK
depends-on:
  - 064-app-shell-titlebar-sidebar-subbar-rail
  - 065-core-component-library
  - 036-get-dashboard-me
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 3 (Dashboard — Me: rolling 7d/30d avg score, trend sparkline, recent sessions list, score detail on click, contributor weight indicator). `DESIGN.md` "KPI tile" (Me view KPIs: sessions · acceptance % · avg score · time saved) + "Session list — three layout variants" (table is default).

## What to build

The Me view — the first real data screen. Table layout only; cards + feed land in 071.

1. **Route binding**: `Route.me` renders this view in the main pane.
2. **Data fetch**: `GET /v1/dashboard/me` via `IterHTTPClient`; cached 30s per `ARCHITECTURE.md` §6 "State management".
3. **KPI row** at top: 4 tiles from `DESIGN.md` "KPI tile" — `sessions · acceptance % · avg score · time saved`. Each with label / value / delta chip / 22px sparkline.
4. **Section header** "Recent sessions" with mono count chip + "View all" link to `Route.sessions`.
5. **Session table** (component from 065) bound to `dashboard.recent_sessions`. Columns per `DESIGN.md`: `Started · Repo·Task · Harness · Dur · Score · Status · Accepted`.
6. **Row click** routes to `Route.sessionDetail(id)` — destination implemented in 072.
7. **Right rail** (296px) with `RailCard`s:
   - "Refinements you contributed" (stub data if not yet served)
   - "Suggestions waiting" — primary action **"Copy to clipboard"**
   - "Active stack" (stub)
8. **State handling per `ARCHITECTURE.md` §6 "Default-state handling"**:
   - Empty dashboard (new user, no scored sessions): "First scored session estimated in `<X>` hours" — never "no data"
   - Loading: skeleton rows in the table
   - Error: inline retry banner
9. **Refetch on focus** + manual refresh button in the subbar (sans, 22px, `⌘R` kbd hint).

## Acceptance criteria

- [ ] `GET /v1/dashboard/me` wired and cached 30s
- [ ] KPI row renders 4 tiles with the correct labels and 22px sparklines
- [ ] Session table renders ≥10 rows from real backend data (against staging)
- [ ] Row click navigates to `Route.sessionDetail(id)` (destination may be a stub)
- [ ] Right rail shows three rail cards including a "Copy to clipboard" action
- [ ] Empty / loading / error states render per §6 rules
- [ ] Refetch on focus + manual refresh both work; cache TTL respected

## Blocked by

- Blocked by `issues/064-app-shell-titlebar-sidebar-subbar-rail.md`
- Blocked by `issues/065-core-component-library.md`
- Blocked by `issues/in-progress/036-get-dashboard-me.md`

## User stories addressed

Screen 3 — Dashboard / Me.
