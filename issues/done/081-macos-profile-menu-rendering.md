# macOS profile menu rendering follow-up

## Problem

The workspace profile switcher is implemented as a dropdown, but the current macOS
menu styling collapses the custom label in the sidebar. The visible result looks
like a bare chevron, so the profile/name cleanup is not obvious in the app.

## Scope

- Keep the profile switcher as a dropdown.
- Make the visible sidebar control render the workspace label and account label.
- Preserve the no-raw-UUID rule for the account label.
- Verify the app builds.

## Resolution

- Removed the macOS menu style that collapsed the custom sidebar label.
- Reused dashboard identity when it has a display name or email.
- Sanitized token/account identity fallbacks so UUIDs and WorkOS `user_...`
  subjects render as `Account` instead.
- Verified with `swiftlint lint mac/IterApp`, a Debug `xcodebuild`, and a live
  app relaunch/screenshot inspection.
