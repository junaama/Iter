---
type: AFK
depends-on:
  - 034-tenant-context-middleware
  - 049-postgres-connection-layer
---

## Parent PRD

`ARCHITECTURE.md` §5 "REST endpoints" + §1.3 "Anti-screens" — note the explicit "no NL search across sessions" invariant from `CLAUDE.md`. This endpoint is **structured filter only**, not NL.

## What to build

`GET /v1/sessions?filter=...` — paginated list of sessions for the current tenant, filtered by structured query parameters.

Supported query params:

- `user_id` — restrict to one user (default: all users in tenant; non-admins forced to their own `user_id` regardless of param)
- `harness` — exact match (`claude_code`, `codex`, `pi`, `opencode`, `gemini_cli`)
- `started_after`, `started_before` — RFC3339 timestamps
- `min_score`, `max_score` — composite score range, joined to latest `session_scores` row per session
- `has_outcome` — `commit_landed | pr_merged | pr_reverted | code_reverted_within_7d | tests_passed | tests_failed | incident_caused | peer_referenced`
- `classification` — `clean | strippable | dirty` (admin only — members can't see `dirty` sessions)
- `cursor` — opaque pagination token
- `limit` — default 25, cap 100

Response: `{ "sessions": [...session-summary...], "next_cursor": "..." }`. Session summary is the same shape as in `GET /v1/dashboard/me`'s `recent_sessions`.

NOT supported (per anti-screen rule): free-text search over `redacted_prompt`. Anything that smells like NL search is rejected at validation. If `q` or similar param appears: 400 with `{"error":"nl_search_not_supported","see":"ARCHITECTURE.md#anti-screens"}`.

## Acceptance criteria

- [ ] Endpoint registered behind auth + tenant-context middlewares
- [ ] All listed filter params supported; invalid combinations → 400 with details
- [ ] Non-admin `user_id` param is silently overridden to the principal's own user_id (so a member can't enumerate other members)
- [ ] `classification=dirty` returns 403 for non-admins
- [ ] Cursor pagination uses an opaque encoded `(started_at, id)` tuple — not a numeric offset; tests verify cursor stability across inserts
- [ ] `q` / `query` / `search` parameters → 400 with NL-search rejection
- [ ] N+1-free even with the score-range filter (one query with a LATERAL join or a CTE)
- [ ] Integration tests cover the matrix above
- [ ] Wire types added to `pkg/contracts/`
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/034-tenant-context-middleware.md`
- Blocked by Step 3 storage-layer baseline — sessions + session_scores + outcomes repository functions

## User stories addressed

Team lead filters sessions by harness or outcome to find good examples to share; admin filters to find leak-suspect (dirty) sessions.
