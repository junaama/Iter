---
type: AFK
depends-on:
  - 034-tenant-context-middleware
  - 049-postgres-connection-layer
---

## Parent PRD

`ARCHITECTURE.md` §5 + §6 "Screen inventory" (Dashboard / Team). Admin-only by default; non-admins get a 403 from the role-check.

## What to build

`GET /v1/dashboard/team` — team-wide aggregates for the current tenant.

Response shape (mirror `contracts.py`):

```json
{
  "members": [
    { "user_id": "...", "display_name": "...", "session_count_30d": 42, "mean_composite_score_30d": 0.64 },
    ...
  ],
  "top_patterns": [
    { "pattern_id": "...", "preview": "...", "uses_count": 12, "tenants_used": 1, "avg_score": 0.71 },
    ...
  ],
  "invite": { "enabled": true, "invite_link_template": "https://iter.dev/invite?..." }
}
```

Rules:

- **Admin gate** — only principals with role `owner` or `admin` (from `tenant_users`) see this endpoint. Member role → 403 with `{"error":"forbidden","required_role":"admin"}`.
- "Former member" handling per `ARCHITECTURE.md` §6: deleted users in the aggregates show `display_name: "former member"`. Implement via a LEFT JOIN on `users` + COALESCE.
- Top patterns surface up to 10 `suggestions` rows ordered by `hit_count * accept_count / NULLIF(hit_count,0)` (acceptance-weighted). Cap to suggestions accessed in the last 30d.
- `invite` block — only present when `enabled: true`; UI gates the button on this flag. Source of truth is a `tenant_settings` column (add to a migration if missing — note this in DECISIONS.md if you need to migrate).

## Acceptance criteria

- [ ] Endpoint registered; admin-only enforced via a role-check helper in `internal/api/principal.go`
- [ ] 403 for non-admins; 200 with member list for admins
- [ ] Former-member display name handled correctly (test: soft-delete a user, verify response shows "former member")
- [ ] 30-day window applied consistently across members + top_patterns
- [ ] N+1-free; verify with `EXPLAIN ANALYZE` and a seeded dataset of 50 users × 1000 sessions
- [ ] Pagination via `?member_limit=N&pattern_limit=N` (cap 100 each)
- [ ] Integration tests cover: admin (200), member (403), former-member rendering, empty tenant (200 with empty arrays)
- [ ] Wire types added to `pkg/contracts/`
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/034-tenant-context-middleware.md`
- Blocked by Step 3 storage-layer baseline — users + tenant_users + session_scores + suggestions repository functions

## User stories addressed

Team lead opens the team dashboard to evaluate the team's coding-agent practice.
