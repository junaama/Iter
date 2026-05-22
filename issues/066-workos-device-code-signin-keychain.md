---
type: AFK
depends-on:
  - 063-xcode-project-design-tokens-theming
  - 056-workos-auth-jwt-verifier
---

## Parent PRD

`ARCHITECTURE.md` §5 "Auth" (WorkOS device-code flow, tokens stored in macOS Keychain, `tenant_id` claim). §6 Screen 1 step 1 (sign in). §9 Step 8 onboarding wizard.

## What to build

Mac app sign-in flow — the user runs `iter login` (or clicks "Sign in" on first launch), gets a device code, completes auth in the browser, app stores the resulting tokens in macOS Keychain and decodes the `tenant_id` claim for use everywhere.

1. **Device-code initiation**: Mac app calls WorkOS device-authorization endpoint, displays the `user_code` + verification URL in a sign-in card with `Open in browser` primary button.
2. **Polling**: poll for token issuance with exp backoff; show a spinner until success/timeout.
3. **Token storage**: access + refresh tokens stored in macOS Keychain under `dev.iter.IterApp` service. Refresh tokens marked `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`.
4. **Token decode**: a `SessionStore` observable exposes `accessToken`, `tenantId`, `userId`, `expiresAt`, `displayName`. Decoded from JWT claims; never re-fetched on every read.
5. **Refresh-on-expiry**: 60s before expiry, kick a background refresh. On refresh failure, surface "session expired, sign in again" and clear Keychain.
6. **`Authorization: Bearer …` header** injected into every `/v1/*` HTTP call via a shared `IterHTTPClient`.
7. **Sign-out**: clears Keychain entries and routes back to sign-in.
8. **CLI parity**: same Keychain entries readable by the existing `iter` CLI so `iter login` from the terminal and the app's sign-in flow are interchangeable.

## Acceptance criteria

- [ ] Device-code flow completes end-to-end against the staging WorkOS app
- [ ] Tokens persist across app restarts (Keychain verified via `security find-generic-password`)
- [ ] `SessionStore.tenantId` populated correctly from the JWT `tenant_id` claim
- [ ] Every `/v1/*` request carries the bearer header (verified against `058`-provisioned staging)
- [ ] 401 from server triggers a refresh; if refresh fails, app routes to sign-in
- [ ] Sign-out clears Keychain entries
- [ ] Concurrent CLI `iter login` and app sign-in read the same Keychain entries

## Blocked by

- Blocked by `issues/063-xcode-project-design-tokens-theming.md`
- Blocked by `issues/done/056-workos-auth-jwt-verifier.md`

## User stories addressed

Screen 1 step 1 — sign in (WorkOS device code). Required before any authenticated screen renders.
