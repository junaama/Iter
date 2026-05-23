# Iter — Design

Visual language and locked design tokens for Iter v1. Source of truth for the SwiftUI Mac app implementation (`ARCHITECTURE.md` §6).

## Reference prototype

`design/dashboard-prototype/` is a Claude-Design HTML/React export covering three of the nine screens in §6:

| Prototype file | Maps to ARCHITECTURE §6 |
|---|---|
| Me view (in `views.jsx`) | Screen 3 — Dashboard / Me |
| Team view | Screen 4 — Dashboard / Team |
| Session detail | Screen 5 — Session detail |

The prototype is **reference, not production code**. Match the visual output pixel-for-pixel in SwiftUI; don't port React/CSS structure literally. Don't render the prototype in a browser unless asked — every token, dimension, and rule is in the source files. See `design/dashboard-prototype/README.md`.

Screens **not yet designed**: stack/simulate, settings. Their layouts must follow the same design language captured below.

Chat transcript at `design/dashboard-prototype/chats/chat1.md` records the intent: "Dense, data-rich, keyboard-driven (Linear-ish)" with "Realistic engineer names + real-sounding repos/tasks."

## Locked tokens

### Typography

| Token | Value |
|---|---|
| Sans family | `IBM Plex Sans` (400 / 500 / 600) |
| Mono family | `IBM Plex Mono` (400 / 500 / 600) |
| Base size | 13px / 1.4 line-height |
| Sans usage | UI chrome, labels, prose |
| Mono usage | All numerics, IDs, timestamps, hex/scores, harness short-codes, keyboard hints, code paths. `font-variant-numeric: tabular-nums`. |

No emoji. No gradients. Monospace is the visual identity for data.

### Color palette — light

| Token | Hex / OKLCH | Use |
|---|---|---|
| `--bg` | `#FBFAF7` | App background (warm off-white) |
| `--panel` | `#FFFFFF` | Cards, table bodies, main content |
| `--sidebar` | `#F4F2ED` | Sidebar, titlebar, table headers |
| `--rail` | `#F9F7F2` | Right rail panel |
| `--border` | `rgba(20,18,14,0.08)` | Standard divider |
| `--border-strong` | `rgba(20,18,14,0.14)` | Hover state, focused outlines |
| `--hover` | `rgba(20,18,14,0.04)` | Row hover |
| `--selected` | `rgba(20,18,14,0.06)` | Active nav, selected segment |
| `--t1` | `#14120E` | Primary text |
| `--t2` | `#5C5851` | Secondary text |
| `--t3` | `#97928A` | Tertiary / muted |
| `--t4` | `#C7C2BA` | Quaternary / disabled |
| `--accent` | `oklch(0.66 0.15 38)` | Coral. The only accent — links, active nav icon, primary buttons, selected card border. |
| `--accent-soft` | `oklch(0.66 0.15 38 / 0.10)` | Accent fills (selected rows, suggestion kind chip) |
| `--good` | `oklch(0.58 0.13 145)` | Score ≥80, landed status, daemon dot |
| `--warn` | `oklch(0.70 0.13 75)` | Score 60–79, review status |
| `--bad` | `oklch(0.58 0.16 25)` | Score <60, dropped status |
| `*-soft` siblings | `... / 0.12–0.14` | Score chip / status chip backgrounds |
| Window shadow | `0 24px 64px rgba(20,18,14,0.18), 0 2px 8px rgba(20,18,14,0.08)` | Mac window |
| Stage backdrop | `#DCD8D1` | Behind the mac window in the prototype |

### Color palette — dark

Triggered by `[data-theme="dark"]`. Same semantic tokens, different values; the implementation must support both via SwiftUI's `colorScheme`.

| Token | Value |
|---|---|
| `--bg` | `#0D0D0F` |
| `--panel` | `#131316` |
| `--sidebar` | `#0A0A0C` |
| `--rail` | `#101013` |
| `--t1` / `--t2` / `--t3` / `--t4` | `#E9E7E2` / `#9B978F` / `#67625B` / `#3E3A35` |
| `--accent` | `oklch(0.72 0.15 38)` (slightly brighter) |
| Stage backdrop | `#050506` |

Full set in `design/dashboard-prototype/project/styles.css:32-54`.

### Harness colors

Single tint per harness, used as 6×6px swatch next to the mono short-code in the `Harness` badge component, and as the fill in card sparklines:

| Harness | `tint` | Short |
|---|---|---|
| `claude-code` | `oklch(0.68 0.14 38)` (coral) | `cc` |
| `codex` | `oklch(0.65 0.11 250)` (blue) | `cx` |
| `opencode` | `oklch(0.62 0.12 145)` (green) | `oc` |
| `gemini` | `oklch(0.68 0.10 295)` (purple) | `gm` |
| `pi` | `oklch(0.70 0.11 75)` (amber) | `pi` |

Add any new harnesses by extending this mapping in lockstep with `contracts.py:Harness`.

### Avatars

Initials in a 4px-rounded square; mono font; tint per teammate seeded from a fixed palette (`av-priya`, `av-mchen`, etc.) so the same person is always the same color. See `styles.css:471-478`.

### Spacing / radius

| Token | Value |
|---|---|
| `--radius` | `8px` (cards, tables, boxed sections) |
| Small radius | `5–6px` (buttons, nav items, pills, segmented controls) |
| `--row-h` | `30px` (table row height — Linear-dense) |
| Card grid min | `308px` (auto-fill) |
| Card padding | `12px 12px 10px` |
| Section gap (vertical) | `18px` |
| Main pane padding | `18px 22px 28px` |
| Rail width | `296px` (Me/Team) or `320px` (Session detail) |
| Sidebar width | `224px` |

## Layout shell

```
┌─────────────────────────────────────────────────────────────────────┐
│ ● ● ●     iter — Workspace             ⌘K · daemon-status-pill      │ ← titlebar 38px
├──────────┬──────────────────────────────────────────────────────────┤
│ sidebar  │ subbar 42px — crumbs | tabs | segmented (table/cards/feed)│
│  224px   ├──────────────────────────────────────────────┬───────────┤
│          │ main-pane                                    │ rail      │
│          │   (KPI row → section → table/cards/feed)     │ 296/320px │
│          │                                              │           │
└──────────┴──────────────────────────────────────────────┴───────────┘
```

- **macOS window chrome**: use the native close/minimize/zoom controls only; the in-app titlebar keeps the mono-style title centered with daemon-status pill + ⌘K + theme toggle right (`styles.css:96-146`).
- **Sidebar**: workspace switcher/profile menu → nav (Me / Team / Sessions / Stack) → active-stack pills → recent sessions → daemon footer with green pulse dot. The profile menu shows the user's name or email address, never a raw subject UUID. Nav item active state: `--selected` bg, t1 text, accent icon, font-weight 500.
- **Subbar segmented control**: `Table · Cards · Feed` — layout variant is **content-level**, not per-screen. Tweaks panel mirrors this control.
- **Right rail**: dropped when in Session detail's two-column grid; present on Me/Team views as `--rail` background with contextual cards (refinements, suggestions, active-now teammates).
- The shell composes as **one Mac window** (`max-width: 1480px; max-height: 940px`). SwiftUI implementation owns its own real window chrome — but the internal layout grid and dimensions are binding.

## Core components

Visual specs locked. SwiftUI names suggested; final naming follows the SwiftUI conventions of `swiftui-expert-skill`.

### Menubar dropdown
- The `NSStatusItem` renders a mono lowercase `i`: coral when connected/running, warn-tinted when paused, and muted with strikethrough when disconnected.
- Popover width is 304px, uses the same panel token, 1px dividers, dense 32-42px rows, and no card-in-card framing.
- Header reads `Iter — running`, `Iter — paused`, or `Iter — disconnected` with the standard good/warn/bad status dot and daemon version chip.
- Activity rows show either `Currently ingesting` plus the daemon task or `Idle since` plus a relative time, followed by `Last session captured`.
- Commands are icon-leading rows: Open Dashboard, Share my stack, Pause/Resume capture, Quit Iter, and a bottom Settings row. Open Dashboard routes to Me; Share my stack routes to Stack and opens the share sheet; Settings routes to `Route.settings`.
- Daemon status is refreshed once on install for icon state and every 5s only while the dropdown is open. Closing the popover cancels that polling task.

### Onboarding sign-in card
- First launch shows a single centered WorkOS device sign-in card using the same panel, border, 8px card radius, IBM Plex, and coral primary button tokens as the dashboard shell.
- Before authorization starts, the primary action reads "Sign in". After the device-code request succeeds, the card shows the mono `user_code`, the verification URL, an `Open in browser` primary button, and a small spinner while polling.
- Expired sessions reuse this card with the message "Session expired. Sign in again to continue." Sign-out routes back to this same view after clearing Keychain.

### Score chip — `Score(value: Int)`
- 30px min width, mono, `padding: 1px 6px`, radius 4.
- Thresholds (binding): `≥80` → `good`, `60–79` → `warn`, `<60` → `bad`.
- Background = `*-soft`, text = `*` (e.g. `good-soft` + `good`).
- Used in tables, cards, feed, session detail header.

### Status chip — `StatusChip(status)`
- 6px round dot + mono label. Three states matching `OutcomeType`-derived rollup:
  - `landed` → `good` dot, "landed"
  - `review` → `warn` dot, "review"
  - `dropped` → `bad` dot, "dropped"

### Harness badge — `Harness(id)`
- 6px square swatch (1.5px radius) tinted per the table above + mono short code (`cc`, `cx`, `oc`, `gm`, `pi`).
- Width fits content; rendered in row "Harness" column and on cards.

### KPI tile (`.kpi`)
- 4-up grid (`repeat(4, 1fr)`) inside a `--radius` bordered container with 1px gaps acting as dividers.
- Label (11px, t2), value (22px mono, weight 500, optional unit at 12px t3), delta chip (up/dn/flat colored), and a 22px sparkline.
- KPIs on Me view: **sessions · acceptance % · avg score · time saved**. Team view substitutes team aggregates.

### Session list — three layout variants
Same row data, three presentations. The current variant is global (controlled by subbar segmented + tweaks panel).

1. **Table** (default — Linear-dense)
   - Columns: `Started · Repo·Task (or Author·Repo·Task on Team) · Harness · Dur · Score · Status · Accepted (n/tools)`.
   - Row height 30px, hover `--hover`, selected `--accent-soft`.
   - Mono for `when`, `dur`, `accepted`; sans for task; t2 for `repo`.
2. **Cards** — `cards-grid` auto-fill min 308px. Per card: `repo + score`, `task` (sans 13px weight 500), `meta` row (harness · dur · tools · accepted), 14px tinted bar mini-sparkline using harness tint, footer with status + relative time.
3. **Feed** — day-grouped (`Today / Yesterday / Mon …`) with uppercase t3 day headers, 4-column row `when · avatar · body · right-actions`.

### Trace timeline — `TraceRow(kind, label, detail)`
Session detail's spine. Kinds and their chip color:

| `kind` | chip bg | use |
|---|---|---|
| `start` / `end` / `commit` | `good-soft` / `good` | session lifecycle, landed commits |
| `prompt` / `suggest` / `extract` | `accent-soft` / `accent` | prompts and refinements |
| `tool` | `selected` / `t2` | tool calls (`read_file`, `grep`, `edit_file`, `run_tests`) |
| `subagent` | `warn-soft` / `warn` | collapsed subagent block; `children: N` → "N tool calls · expand" |

Row layout: `52px (time) · 20px (kind chip) · 1fr (body: bold label + mono detail)`. Border bottom is **dashed**. Subagent rows collapse by default — matches `ARCHITECTURE.md` §6 anti-screen rule "subagents collapsed into parent in list view; expandable."

### Outcome grid (Session detail)
2×2 mono panels: `tests · commits · files · tools used` derived from the session's outcome events. Value at 15px mono, label at 11px t2.

### Rail cards (`.rail-card`)
- Used for "Refinements you contributed", "Suggestions waiting", "Active stack", "Active now" (Team).
- White panel, bordered, header in 11.5px weight-600 t1 with a mono count chip on the right.
- Each item has title (12px t1), meta line (mono 10.5px t3 with `·` separators), and optional action buttons.
- Suggestion item primary action wording is **"Copy to clipboard"** — never "Replace" (binds `ARCHITECTURE.md` §6 UX answers).

### Suggestion popover
- Native macOS notification, not an in-window card. Title is "Iter suggestion"; body is the refined prompt preview capped near 120 characters.
- Actions are ordered: **"Copy to clipboard"**, "Dismiss", "Suppress this pattern". The copy action writes `refined_prompt` to the pasteboard and performs no terminal automation.
- Notification clicks open a compact floating panel using the same panel background, mono prompt text, rationale, and up to three evidence rows. It auto-clears after 8s if untouched.

### Stack / Me
- Main pane uses the existing dense dashboard shell: editable stack name, status pill, read-only harness chips, skills rows, doc reference rows, notes textarea, and compact Save/Share actions.
- Harness chips keep the existing tint palette; detected harnesses use dashed 1px borders, explicit harnesses use solid tinted 1px borders.
- Share sheet uses the same panel token and lists file references as checkboxes before the Whole team and Specific teammate actions. Deselecting a file reference removes it from the share payload.
- The right rail swaps the generic suggestion cards for share grants plus Shared with me cards using existing avatar and rail-card tokens.

### Settings
- Settings is a rail-less main-pane route reached from the sidebar or menubar.
- Sections render as vertical bordered `--radius` panels: Account, Tenant, Capture toggles, Retention info, Redaction rules preview, Data export, Notifications.
- Per-harness capture rows use the canonical harness tint + mono short-code and round-trip through the local daemon IPC. Tenant-default values must show a source badge; local overrides show `this Mac`.
- Retention copy is read from the app's `SettingsPolicy.retentionSummary` source instead of duplicating the literal in row components.
- Account export calls `POST /v1/account/export`, then polls `GET /v1/account/export/{id}` until the status is `ready` or `failed`. Ready responses expose a download URL or archive pointer. Account deletion calls `POST /v1/account/delete` after the shared destructive confirmation sheet requiring `DELETE`.

### Buttons (`.btn`, `.btn.primary`)
- 22px tall, sans, 11px text, 5px radius.
- Default: panel bg, t1 text, 1px border.
- Primary: accent bg, white text, no border. Hover darkens to `oklch(0.62 0.16 38)`.
- Inline `kbd` hint in mono 10px, t3 (or white@0.8 on primary).

### Segmented control
Inline-flex 26px tall, mono 11px, 1px panel border with internal `border-right` dividers. Active segment uses `--selected`.

### Search input
26px tall, 220px wide, mono `⌘K` kbd hint in the right corner, search icon left.

## Scoring model

From the chat transcript: composite score is **0–100, derived from `tests_passed × commits_landed × suggestions_accepted`**. The visual chip thresholds (≥80 / 60–79 / <60) are binding. This is the UI-facing rollup of the more granular `composite_score` (0.0–1.0) in `contracts.py:CompositeScoreOutput` — multiply by 100, round, render.

## Tweaks panel (developer-mode toggle, not shipped UI)

The prototype's tweaks panel exposed: dark mode, accent swatch (4 options), layout variant, jump-to-view buttons. **Dark mode and layout variant persist into v1**; accent swatches and jump-to-view buttons were prototype-time exploration. Accent stays locked at coral.

## Reinforced product rules

These appear in the visual layer and must not be implemented away:

- **Suggestion actions are clipboard-only** — primary button reads "Copy to clipboard" (`ARCHITECTURE.md` §6, `chat1.md`).
- **No emoji, no gradients** — anywhere in the UI.
- **Mono for all numerics, IDs, timestamps** — including filenames and branches when shown.
- **Single accent** — coral. Status colors (good/warn/bad) are not accents; they are semantic.
- **Linear-dense rows** — 30px row height in tables. Don't loosen to "comfortable" for any tenant.
- **Daemon footer is always visible in sidebar** — green pulse dot indicates running; warn dot indicates paused.
- **Score chip thresholds** are referenced as a refinement in the data (`Score chip thresholds: 0–59 red, 60–79 amber, 80+ green`). Treat as binding.

## When updating designs

- Edits to design tokens go in **both** this file and any future SwiftUI token file in lockstep.
- SwiftUI design tokens live in `mac/IterApp/DesignSystem/`; update those files in lockstep with this section.
- New screens (the 6 remaining from §6) reuse these tokens; do not introduce new colors, type sizes, or radii without updating this doc first.
- If a token changes, search the SwiftUI source for hardcoded values that should have been the token.
