---
type: AFK
depends-on:
  - 034-tenant-context-middleware
  - 014-postgres-connection-layer
---

## Parent PRD

`ARCHITECTURE.md` §5 "REST endpoints" + §6 "Screen inventory" (Dashboard / Me). Powers the SwiftUI Me screen (`design/dashboard-prototype/`).

## What to build

`GET /v1/dashboard/me` — returns the authenticated user's personal aggregates for their current tenant. Read-only; ≤200ms P99.

Response shape (mirror `contracts.py`; if it doesn't exist there yet, add the Pydantic model in the same commit and mirror in `pkg/contracts/`):

```json
{
  "user": { "id": "...", "display_name": "...", "email": "..." },
  "trend": [
    { "date": "2026-05-15", "composite_score": 0.62, "session_count": 4 },
    ...
  ],
  "recent_sessions": [
    { "id": "...", "started_at": "...", "composite_score": 0.71, "harness": "claude_code", "redacted_prompt_preview": "first 120 chars..." },
    ...
  ]
}
```

Queries:
1. `users` row for the principal (already known from JWT but include `display_name` + `email` in the response).
2. Trend: 14 days of `session_scores` aggregated per day for this user; `composite_score` is the mean weighted by `contributor_weight`.
3. Recent sessions: top 10 by `started_at DESC` for this user.

All queries scoped by `tenant_id` automatically via the tenant-context middleware (019) — RLS guarantees safety.

## Acceptance criteria

- [ ] Endpoint at `GET /v1/dashboard/me`; behind auth + tenant-context middlewares
- [ ] Trend window is 14 days; configurable via `?days=N` (cap at 90, the hot retention window)
- [ ] Recent sessions limited to 10 by default; configurable via `?limit=N` (cap at 50)
- [ ] `redacted_prompt_preview` is the first 120 characters; longer prompts get an ellipsis
- [ ] N+1-free — single query for trend, single query for recent sessions (or a CTE)
- [ ] 200ms P99 verified via test with a seeded dataset of ~500 rows per user
- [ ] Integration test against testcontainers Postgres
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/034-tenant-context-middleware.md`
- Blocked by Step 3 storage-layer baseline — sessions / session_scores repository functions

## User stories addressed

Adam opens the Mac app and sees his own learning trajectory.
