---
type: AFK
depends-on: []
---

# AFK — SwiftUI project + design tokens

This issue is AFK-ready with Xcode, `xcodebuild`, `xcbeautify`, and `swiftlint` available in the terminal. Build a local development app and design-token foundation without requiring App Store/Xcode account clicks. Developer ID signing, notarization credentials, and release signing remain in issue 078; for this slice, use an unsigned or local/ad-hoc development build if a Developer ID team is not available from the CLI.

## Parent PRD

`ARCHITECTURE.md` §6 (UI/UX shell), §9 Step 8 ("SwiftUI app: Xcode project, codesign + notarize, launchd plist for daemon, menubar item, light/dark theming"). `DESIGN.md` "Locked tokens" + "Reinforced product rules".

## What to build

Foundational Mac app target — a runnable SwiftUI window that wires up the locked design tokens and supports light/dark theming. Nothing else; this slice exists so every subsequent slice has a place to put code.

1. **Xcode project** under `mac/` (new top-level dir): SwiftUI macOS app, deployment target = current macOS minus one (Sequoia at the time of writing). Bundle ID `dev.iter.IterApp`.
2. **Development signing**: project builds locally and produces an unsigned or local/ad-hoc `.app` for `make mac-dev`. If a Developer ID identity/team is discoverable from the CLI, document how the project is configured to use it later; do not block this slice on interactive Apple account setup.
3. **Fonts**: `IBM Plex Sans` (400/500/600) + `IBM Plex Mono` (400/500/600) bundled in `Resources/Fonts/` and registered via `Info.plist` `ATSApplicationFontsPath`.
4. **Tokens as Swift code** in `mac/IterApp/DesignSystem/`:
   - `Colors.swift` — every token from `DESIGN.md` "Color palette — light" + "— dark" as `Color` extensions, resolved via `colorScheme` environment. Harness tints + avatar tints included.
   - `Typography.swift` — `IterFont.sansBody`, `.monoBody`, etc.; `font-variant-numeric: tabular-nums` enforced for the mono variants.
   - `Spacing.swift` / `Radius.swift` — `radius = 8`, `rowHeight = 30`, `sidebarWidth = 224`, `railWidth.me = 296`, `railWidth.session = 320`, gaps and paddings per `DESIGN.md` "Spacing / radius".
5. **Theme switching**: a `ThemeStore` observable + a debug-only menu item to toggle light/dark; persisted in `UserDefaults`. SwiftUI `colorScheme` honored.
6. **Hello window**: launches a 1480×940 max window with the warm off-white `--bg` (light) / `#0D0D0F` (dark) background and the title `iter — Workspace` in mono — proves tokens render and theming works.
7. **Makefile target** `make mac-dev` builds + launches the app.

## Acceptance criteria

- [ ] `mac/IterApp.xcodeproj` builds from the CLI with no warnings under both light and dark `colorScheme`
- [ ] IBM Plex Sans + Mono load and render (verify by inspecting the title)
- [ ] Every color/spacing/radius/typography token in `DESIGN.md` exists as a Swift symbol
- [ ] Toggling theme flips palette without restart and persists
- [ ] `make mac-dev` builds and launches the app, or in headless verification builds the app and documents the launch command
- [ ] No emoji, no gradients, no third-party UI deps (matches `DESIGN.md` "Reinforced product rules")
- [ ] `DESIGN.md` "When updating designs" note added: tokens live in `mac/IterApp/DesignSystem/`

## Blocked by

None — can start immediately.

## User stories addressed

Foundation for every other Step 8 slice. No standalone user story.
