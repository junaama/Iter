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
  - 061-domain-dns-iter-dev
---

# HITL — requires Apple Developer account, notarization credentials, and iter.dev hosting

Notarization requires `xcrun notarytool` with an app-specific password tied to a real Apple Developer Program membership. Uploading the DMG to iter.dev requires the domain (061). AFK workers should skip.

## Parent PRD

`ARCHITECTURE.md` §1 "Core user actions" #1 ("Download Mac app from iter.dev"). §9 Step 8 ("Xcode project, codesign + notarize"). CLAUDE.md "Distributed via iter.dev."

## What to build

The shippable DMG and the download page on iter.dev.

1. **Hardened runtime + entitlements**: enable hardened runtime; entitlements for Accessibility, Full Disk Access scoped to harness dirs, and outbound network. No JIT, no debugger.
2. **Codesigning**: Developer ID Application certificate; both `Iter.app` and the embedded `iter-daemon` binary signed.
3. **Notarization**: `xcrun notarytool submit Iter.dmg --wait`; staple the ticket with `xcrun stapler staple Iter.dmg`. Verify with `spctl --assess -vvv Iter.app`.
4. **DMG packaging**: drag-to-Applications layout, custom background using locked design tokens, `Iter.app` + shortcut to `/Applications`.
5. **Sparkle (or built-in) auto-update**: appcast feed at `https://iter.dev/appcast.xml`. EdDSA-signed releases. Update checks on app launch + every 24h.
6. **Release pipeline**: GitHub Actions workflow `release-mac.yml` triggered by a `v*` tag — builds, signs, notarizes, staples, packages DMG, uploads to iter.dev's R2 bucket, updates appcast.
7. **iter.dev download page**: a static page served from the same Go binary or a static R2 site that detects Apple Silicon vs Intel and serves the correct DMG. SHA-256 published alongside.
8. **Version-drift surface**: bundle version + daemon version surfaced in Settings → Account; matches the version-drift modal from 068.
9. **Release notes**: each release's notes pulled from `CHANGELOG.md`; published on iter.dev and surfaced in the Sparkle update prompt.

## Acceptance criteria

- [ ] `Iter.dmg` is codesigned, notarized, and stapled; `spctl --assess` passes
- [ ] Embedded `iter-daemon` binary is signed and notarized
- [ ] Hardened runtime + entitlements pass `codesign --verify --deep --strict`
- [ ] `release-mac.yml` builds + signs + notarizes + uploads end-to-end from a git tag
- [ ] iter.dev `/download` serves the right DMG by architecture; SHA-256 published
- [ ] Sparkle (or equivalent) appcast served from `iter.dev/appcast.xml`; auto-update verified on a previous build
- [ ] First-time install: download → drag to Applications → launch → onboarding wizard (Slice 067 flow) works on a fresh user account
- [ ] `CHANGELOG.md` + release notes published per release

## Blocked by

- Blocked by every other Step-8 slice (`063`–`077`)
- Blocked by `issues/061-domain-dns-iter-dev.md`

## User stories addressed

`ARCHITECTURE.md` §1 core user action #1 — "Download Mac app from iter.dev".
