---
type: AFK
depends-on:
  - 070-dashboard-me-table
---

## Parent PRD

`DESIGN.md` "Session list — three layout variants" (2. Cards, 3. Feed) + "Subbar segmented control" note that "layout variant is **content-level**, not per-screen".

## What to build

Wire the Me view's session list to the subbar segmented control so the user can swap between table / cards / feed.

1. **`LayoutVariantStore`** (introduced in 064) now actually drives the list rendering. Cards + feed variants render the same `dashboard.recent_sessions` data as the table.
2. **`SessionCards`** component (built in 065) consumes the same data source. Card spec per `DESIGN.md`: `repo + score`, `task`, meta row (harness · dur · tools · accepted), 14px tinted bar mini-sparkline using harness tint, footer with status + relative time.
3. **`SessionFeed`** component (built in 065): day-grouped (`Today / Yesterday / Mon …`) with uppercase t3 day headers, 4-column row.
4. **URL/route persistence**: layout variant persists across app launches in `UserDefaults` (matches `DESIGN.md` "Tweaks panel" — variant is one of the prototype-time toggles that **stays** in v1, though the tweaks panel UI itself does not ship).
5. **Animation**: variant switch crossfades content; subbar control press lands in <100ms.

## Acceptance criteria

- [ ] Subbar segmented `Table · Cards · Feed` switches the Me view list in <100ms
- [ ] Cards layout matches `DESIGN.md` card spec (308px min, 14px tinted bar sparkline, status + relative time footer)
- [ ] Feed layout groups rows by day with uppercase t3 day headers
- [ ] Variant persists across app launches
- [ ] All three variants render the same underlying data; row click target unchanged

## Blocked by

- Blocked by `issues/070-dashboard-me-table.md`

## User stories addressed

Completes Screen 3 — Dashboard / Me.
