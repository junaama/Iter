---
type: AFK
depends-on:
  - 051-repositories-tenancy-sessions
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 (repository functions, continued); §3 Tables (`session_embeddings`, `session_scores`, `outcomes`); §2 "Latency budget for `iter suggest`" — the ANN search via pgvector lives at this layer with a ~50ms budget.

## What to build

The second repository slice. Adds the scoring-adjacent tables, following the conventions established in 051. Critical piece: the HNSW ANN search wrapper.

Tables in scope:

- `session_embeddings` (`vector(1536)`, HNSW index per `migrations/0001_initial.sql`)
- `session_scores` (one or more scoring runs per session)
- `outcomes` (links sessions to downstream git/incident events)

Specifically:

1. **`session_embeddings`**:
   - `Insert(tx, sessionID, embedding []float32) error` — accepts a 1536-dim slice; rejects mismatched dims at the call site (NOT at the SQL level — fast-fail in Go).
   - `Search(tx, query []float32, k int, filter) ([]Result, error)` — wraps `ORDER BY embedding <=> $1 LIMIT k`. Returns session ids + cosine distances. `filter` supports a date-range and an `excluded_session_ids` slice (callers commonly want to exclude the current session).
   - `Delete(tx, sessionID) error` — cascade is wired in the migration; this is for explicit one-off deletes.
   - **HNSW parameter sanity**: per `migrations/0001_initial.sql` the index is `WITH (m=16, ef_construction=64)`. `Search` SHOULD `SET LOCAL hnsw.ef_search` (default 40) configurable via a context value; benchmark before defaulting differently.
2. **`session_scores`**:
   - `Insert(tx, score) error` — `scorer_version` is required (per §7 "scoring bug at scale" → rollback by version).
   - `GetLatestBySession(tx, sessionID) (Score, error)` — returns the most recent score for the session.
   - `ListByVersion(tx, version, filter) ([]Score, error)` — for the nightly batch path.
3. **`outcomes`**:
   - `Insert(tx, outcome) error` — accepts `session_id`, `kind` (`commit`/`pr_merge`/`incident`/...), `external_ref` (e.g. GitHub PR url), `signal` (`positive`/`negative`/`neutral`).
   - `ListBySession(tx, sessionID) ([]Outcome, error)`.
   - `AttachPending(tx, sessionID, outcome) error` — supports the "orphan webhook → pending-outcomes retry 7 days" semantics from §9 Step 5.
4. **Tests** (`internal/db/dbtest/` reused from 051):
   - HNSW round-trip: insert N=100 random embeddings + one designed-to-match embedding; `Search(query, k=5)` returns the matching session id first.
   - HNSW recall sanity: re-insert N=1000 vectors, run a known query, verify the expected top-1 hit. Loose recall assertion (>0.85 per `ARCHITECTURE.md` §7 "pgvector recall degrades").
   - Scores: insert two `scorer_version`s for one session, `GetLatestBySession` returns the newer one.
   - Outcomes: insert + list; pending-outcomes orphan retry handling deferred to Step 4 (webhook handler) but the data shape is here.

## Acceptance criteria

- [ ] `session_embeddings.go` with `Insert` / `Search` / `Delete`; dim check rejects non-1536 inputs
- [ ] `session_scores.go` with `Insert` / `GetLatestBySession` / `ListByVersion`; `scorer_version` required
- [ ] `outcomes.go` with `Insert` / `ListBySession` / `AttachPending`
- [ ] HNSW search test passes (top-1 match on a designed query)
- [ ] HNSW recall sanity test (>0.85 against a 1000-vector seed) passes
- [ ] `scorer_version` round-trip verified; `GetLatestBySession` returns newest
- [ ] `hnsw.ef_search` SET LOCAL behavior documented and configurable
- [ ] `make test` + `make lint` pass

## Blocked by

- Blocked by `issues/051-repositories-tenancy-sessions.md`

## User stories addressed

`POST /v1/suggest` (the latency-critical path) goes through `Search`; the nightly Modal batch writes `session_scores`; the GitHub/Linear webhook handlers write `outcomes`.
