---
type: AFK
depends-on:
  - 051-repositories-tenancy-sessions
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 (repository functions, continued); §3 Tables (`suggestions`, `stacks`, `stack_shares`, `archive_pointers`); §3 "Stacks" + §3 "Retention" for stack scope and archive lifecycle semantics.

## What to build

The third and final repository slice. Same conventions as 051/052. Tables in scope:

- `suggestions` (cached prompt refinements, embedded for similarity lookup)
- `stacks` + `stack_shares` (shareable wrapped solutions)
- `archive_pointers` (Cloudflare R2 object URI pointers for >90d sessions)

Specifically:

1. **`suggestions`**:
   - `Insert(tx, suggestion) error` — includes `source_embedding vector(1536)` (HNSW indexed per `migrations/0001_initial.sql`) and `refined_prompt`.
   - `SearchSimilar(tx, query []float32, k int, minConfidence float64) ([]Suggestion, error)` — ANN search on `source_embedding`. Used by `POST /v1/suggest` to cache-hit on near-duplicate prompts.
   - `Delete(tx, id) error`.
2. **`stacks`**:
   - `Upsert(tx, stack) error` — one stack per user; idempotent on `(tenant_id, user_id)`.
   - `Get(tx, userID) (Stack, error)`.
   - `notes` is plain text (per `DECISIONS.md` "Phase 3 — `notes: text` replaces unbounded JSON").
3. **`stack_shares`**:
   - `Insert(tx, share) error` — `(stack_id, scope, target_user_id?, target_team)`. `target_team` is the whole tenant scope per `DECISIONS.md` "Stack sharing model".
   - `ListByStack(tx, stackID) ([]Share, error)`.
   - `Delete(tx, id) error`.
   - **Cross-tenant guard**: `Insert` rejects `target_user_id` belonging to a different tenant. Test explicitly.
4. **`archive_pointers`**:
   - `Insert(tx, pointer) error` — `(session_id, object_uri, manifest_sha256, archived_at)`. Schema column name is `object_uri` per `CLAUDE.md` "Locked invariants".
   - `Get(tx, sessionID) (Pointer, error)` — used when fetching a >90d session: hit Postgres metadata + redirect to R2 via the pointer.
   - `ListExpiringFromPostgres(tx, cutoff time.Time, limit int) ([]Pointer, error)` — for the archive cron's candidate-selection step.

## Acceptance criteria

- [ ] `suggestions.go` with `Insert` / `SearchSimilar` / `Delete`
- [ ] `stacks.go` with `Upsert` / `Get`; idempotent on `(tenant_id, user_id)`
- [ ] `stack_shares.go` with `Insert` / `ListByStack` / `Delete`
- [ ] `stack_shares.Insert` rejects cross-tenant `target_user_id`; test passes
- [ ] `archive_pointers.go` with `Insert` / `Get` / `ListExpiringFromPostgres`; column is `object_uri`
- [ ] `SearchSimilar` ANN test passes against a 1000-suggestion seed (loose recall, same harness as 052)
- [ ] Tests confirm `archive_pointers.Get` returns the correct pointer for an archived `session_id`
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/051-repositories-tenancy-sessions.md`

## User stories addressed

`POST /v1/suggest` cache-hit path via `suggestions.SearchSimilar`. Stack share screens depend on `stacks` + `stack_shares`. The archive cron (Step 4) writes `archive_pointers`; session-detail UI for >90d sessions reads them.
