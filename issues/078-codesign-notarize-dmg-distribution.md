---
type: HITL
depends-on:
  - 063-xcode-project-design-tokens-theming
  - 064-app-shell-titlebar-sidebar-subbar-rail
  - 065-core-component-library
  - 066-workos-device-code-signin-keychain
  - 067-macos-permissions-tenant-handshake
  - 068-daemon-launchd-unix-socket-ipc
  - 069-menubar-dropdown
  - 070-dashboard-me-table
  - 071-dashboard-me-cards-feed
  - 072-session-detail
  - 073-dashboard-team
  - 074-stack-me
  - 075-stack-simulate-teammate
  - 076-suggestion-popover
  - 077-settings
---

# HITL — requires Apple Developer account and notarization credentials

Notarization requires `xcrun notarytool` with an app-specific password tied to a real Apple Developer Program membership. Custom `iter.dev` DNS is deferred to issue 061; until then, release artifacts can be uploaded to R2/Railway-generated hosting. AFK workers should skip.

## Parent PRD

`ARCHITECTURE.md` §1 "Core user actions" #1 ("Download Mac app from iter.dev"). §9 Step 8 ("Xcode project, codesign + notarize"). CLAUDE.md "Distributed via iter.dev."

## What to build

The shippable DMG and the download page on generated hosting, with custom `iter.dev` cutover deferred to issue 061.

1. **Hardened runtime + entitlements**: enable hardened runtime; entitlements for Accessibility, Full Disk Access scoped to harness dirs, and outbound network. No JIT, no debugger.
2. **Codesigning**: Developer ID Application certificate; both `Iter.app` and the embedded `iter-daemon` binary signed.
3. **Notarization**: `xcrun notarytool submit Iter.dmg --wait`; staple the ticket with `xcrun stapler staple Iter.dmg`. Verify with `spctl --assess -vvv Iter.app`.
4. **DMG packaging**: drag-to-Applications layout, custom background using locked design tokens, `Iter.app` + shortcut to `/Applications`.
5. **Sparkle (or built-in) auto-update**: appcast feed at a generated release URL for now, documented so it can move to `https://iter.dev/appcast.xml` when 061 lands. EdDSA-signed releases. Update checks on app launch + every 24h.
6. **Release pipeline**: GitHub Actions workflow `release-mac.yml` triggered by a `v*` tag — builds, signs, notarizes, staples, packages DMG, uploads to the release bucket/hosting target, updates appcast.
7. **Download page**: a static page served from the same Go binary, Railway-generated domain, or a static R2 site that detects Apple Silicon vs Intel and serves the correct DMG. SHA-256 published alongside. Custom `iter.dev/download` is a follow-up in 061.
8. **Version-drift surface**: bundle version + daemon version surfaced in Settings → Account; matches the version-drift modal from 068.
9. **Release notes**: each release's notes pulled from `CHANGELOG.md`; published on iter.dev and surfaced in the Sparkle update prompt.

## Acceptance criteria

- [ ] `Iter.dmg` is codesigned, notarized, and stapled; `spctl --assess` passes
- [ ] Embedded `iter-daemon` binary is signed and notarized
- [ ] Hardened runtime + entitlements pass `codesign --verify --deep --strict`
- [ ] `release-mac.yml` builds + signs + notarizes + uploads end-to-end from a git tag
- [ ] Generated download URL serves the right DMG by architecture; SHA-256 published
- [ ] Sparkle (or equivalent) appcast served from the generated release URL; auto-update verified on a previous build
- [ ] First-time install: download → drag to Applications → launch → onboarding wizard (Slice 067 flow) works on a fresh user account
- [ ] `CHANGELOG.md` + release notes published per release

## Blocked by

- Blocked by every other Step-8 slice (`063`–`077`)
- Custom `iter.dev` DNS in `issues/deferred/061-domain-dns-iter-dev.md` is not a blocker for generated-domain distribution.

## User stories addressed

`ARCHITECTURE.md` §1 core user action #1 — "Download Mac app from iter.dev".
