# Agent Dashboard

_Started 2026-05-21 02:06 UTC_

---

## User

im building a Mac app for teams using coding agents. It wraps native terminal harnesses (Claude Code, Codex, Pi, OpenCode, Gemini), captures agent traces locally, scores outcomes, and surfaces prompt refinements at task-start time — so a teammate's learnings show up in your next session as a better prompt suggestion. Distributed via iter.dev, agent-led queries powered by CLI + skill.md. create the dashboard view.

## Assistant

_[tool: questions_v2]_

Before I dive in, a few quick questions to nail the direction — your decisions doc is great context but the dashboard could go several ways.

## User

Questions answered:
- scope: Both + session detail as a third view
- chrome: Full macOS window chrome with sidebar
- aesthetic: Dense, data-rich, keyboard-driven (Linear-ish)
- mode: Both with a toggle
- core_content: Decide for me
- team_content: Decide for me
- options_count: One polished direction
- interactivity: Decide for me
- tweaks: Layout variant (cards vs table vs feed)
- data_voice: Realistic engineer names + real-sounding repos/tasks

## Assistant

_[tool: update_todos]_

Setting direction:
- **Aesthetic:** Linear-dense, dev-tool flavor — IBM Plex Sans + Plex Mono, warm-neutral palette, single coral accent at `oklch(0.68 0.14 38)`. No emoji, no gradients.
- **Stack:** React prototype, full macOS window chrome, three views (Me / Team / Session) navigable via sidebar + clicking rows. Light/dark toggle. Tweaks expose layout variant (table / cards / feed), density, accent.
- **Score model:** since not specified — auto 0–100 derived from `tests_passed × commits_landed × suggestions_accepted`, surfaced as a small monospace chip.

_[tool: copy_starter_component]_

_[tool: read_file]_

_[tool: write_file]_

_[tool: write_file]_

Now I'll build the prototype. Splitting into a few files to keep each manageable.

_[tool: write_file]_

_[tool: write_file]_

_[tool: write_file]_

_[tool: write_file]_

_[tool: write_file]_

_[tool: show_html]_

_[tool: done]_

_[tool: fork_verifier_agent]_

Built the iter dashboard prototype — `index.html`.

**What's there**
- **macOS window chrome** w/ traffic lights, daemon-status pill, ⌘K
- **Sidebar:** workspace switcher, Me / Team / Sessions / Stack nav, active-stack pills, recent sessions, daemon footer
- **Me view:** 4 KPIs (sessions, acceptance %, avg score, time saved), recent sessions, right rail with refinements you've contributed + suggestions waiting (with "Copy to clipboard" wording per your decisions) + active stack pills
- **Team view:** team KPIs, contributors leaderboard, top circulating refinements, full team session feed, right rail with active-now teammates + stacks in use
- **Session detail:** crumbs, header w/ harness + score, outcome grid (tests/commits/files/tools), trace timeline with collapsed subagent + extracted refinement + accepted/dismissed suggestions
- **Three layouts** for session lists: **Table** (default Linear-dense), **Cards**, **Feed** — togglable in the subbar segmented control AND in Tweaks
- **Light + dark** toggle, accent swatches, full keyboard-feel (kbd hints inline)
- Fully clickable: sidebar nav, view tabs, layout switcher, time range, click any session row → detail, back button

**Tweaks panel exposes:** dark mode, accent color (4 swatches), layout variant, plus quick "jump to view" buttons.

**Aesthetic system locked in:** IBM Plex Sans + Plex Mono, warm-neutral palette, coral accent at `oklch(0.66 0.15 38)`, score chips (≥80 green / 60–79 amber / <60 red), no emoji, no gradients, monospace for all numerics/IDs/timestamps.

