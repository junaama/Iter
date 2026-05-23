---
type: AFK
depends-on:
  - 070-dashboard-me-table
  - 039-get-dashboard-team
---

## Parent PRD

`ARCHITECTURE.md` §6 Screen 4 (Dashboard — Team: member aggregates, "top patterns this week" panel, invite button admin-only). `DESIGN.md` "KPI tile" (team aggregates substituted for Me KPIs) + "Session list" (Team table substitutes Author·Repo·Task column).

## What to build

The Team view. Sibling of Me — reuses the table/cards/feed variants and the KPI row, swapping the data shape.

1. **Route binding**: `Route.team`.
2. **Data fetch**: `GET /v1/dashboard/team`; cached 30s.
3. **KPI row**: team aggregates (e.g. team sessions, team acceptance %, team avg score, team time saved). Same `KPIRow` component as Me — only labels and values differ.
4. **Member aggregates table**: one row per teammate with avatar, name, sessions count, acceptance %, avg score, contributor weight indicator.
5. **Recent sessions list**: `SessionTable` / `SessionCards` / `SessionFeed` — Team variant adds `Author` column (Avatar + name) before `Repo·Task`, per `DESIGN.md` "Session list" note "Started · Repo·Task (or Author·Repo·Task on Team)".
6. **Top patterns this week** rail card: list of prompt patterns ordered by acceptance rate / time saved.
7. **Active now** rail card: teammates with an open daemon connection right now (data from WS gateway presence).
8. **Invite teammate** button: rendered **only** when `sessionStore.role == "admin"`. Opens an invite modal (email input → backend invite endpoint — flag if endpoint missing).
9. **Empty / solo-dev fallback**: per `ARCHITECTURE.md` §6 "Default-state handling", solo dev sees a one-row table with "you" and an "invite teammate" CTA.
10. **`LayoutVariantStore`** is shared with Me — switching variant on Team affects Me and vice versa (per `DESIGN.md` "Subbar segmented control" note: variant is content-level).

## Acceptance criteria

- [ ] `GET /v1/dashboard/team` wired and cached 30s
- [ ] Team KPI row renders 4 team-aggregate tiles
- [ ] Member table shows real teammates against staging
- [ ] Session table adds `Author` column (Avatar + name)
- [ ] "Top patterns this week" + "Active now" rail cards present
- [ ] Invite teammate button visible only to admins; modal posts to backend
- [ ] Solo-dev fallback row renders correctly when team size = 1
- [ ] All three layout variants share state with Me

## Blocked by

- Blocked by `issues/070-dashboard-me-table.md`
- Blocked by `issues/in-progress/039-get-dashboard-team.md`

## User stories addressed

Screen 4 — Dashboard / Team.
