---
type: HITL
depends-on:
  - 067-macos-permissions-tenant-handshake
---

# HITL - onboarding admin approval queue

Issue 067 now records first-run join intent from the Mac onboarding flow, but a real approval queue still needs an admin-side product surface and two-account verification.

## What to build

1. Add a persistent tenant join request table with `tenant_id`, `requesting_user_id`, `status`, `requested_at`, `decided_at`, and `decided_by_user_id`.
2. Replace the issue-067 audit-only join request with writes to that table.
3. Add admin endpoints to list, approve, and decline pending requests.
4. On approval, insert the `tenant_users` membership and refresh the requester's Iter session tenant.
5. Push the approval result over the daemon WebSocket so the waiting onboarding screen resolves without a manual relaunch.
6. Verify with two accounts: one existing tenant admin and one new user with the same email domain.

## Acceptance criteria

- [ ] Join requests persist and are visible to tenant admins only.
- [ ] Non-admins cannot approve or decline requests.
- [ ] Approval adds the requester to the tenant and the Mac app exits the waiting state.
- [ ] Decline leaves the requester in their personal workspace and shows a clean state.
- [ ] Two-account manual test recorded in the issue before marking done.
