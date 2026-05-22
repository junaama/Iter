---
type: AFK
depends-on:
  - 034-tenant-context-middleware
  - 014-postgres-connection-layer
---

## Parent PRD

`ARCHITECTURE.md` §5 "REST endpoints" + §6 "Screen inventory" (Session detail). Powers the SwiftUI session-detail screen.

## What to build

Two sibling read endpoints. Bundled because the dashboard fetches them together for the same screen.

### `GET /v1/sessions/:id`

Full session detail for one session_id. Includes:
- Top-level session row (sessions table)
- All `session_events` rows for the session, ordered by `occurred_at ASC`
- Subagent sessions (children where `parent_session_id = :id`) — recursively, depth-limited to 5
- Linked outcomes

Response is one composite envelope. 404 if not found (or RLS-hidden — indistinguishable to the caller is intentional).

### `GET /v1/scores/:session_id`

All scoring runs for the session, ordered by `scored_at DESC`. Surfaces `scorer_version`, `composite_score`, `signals` (jsonb), `rationale`, `contributor_weight`. Shows scoring history so the UI can show "scored by v0.3 → v0.4."

## Acceptance criteria

- [ ] Both endpoints registered behind auth + tenant-context middlewares
- [ ] 404 (NOT 403) when the session belongs to another tenant — leaking existence is itself a leak
- [ ] Subagent recursion depth-limited; deeper trees return what we have plus a `truncated: true` flag
- [ ] Events list is unbounded by default but capped at 10K rows per response with a pagination cursor (rare in practice; large traces exist)
- [ ] Outcomes joined or returned as a sibling array — pick one and document
- [ ] N+1-free — recursive CTE for subagent tree, single query for events
- [ ] Wire types mirrored from `contracts.py` to `pkg/contracts/`
- [ ] Integration tests cover: own session (200), other tenant's session (404), nested subagent tree, truncation at depth 5
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/034-tenant-context-middleware.md`
- Blocked by Step 3 storage-layer baseline — sessions + session_events + session_scores + outcomes repository functions

## User stories addressed

Adam (or a team lead) drills into a session to understand what happened.
