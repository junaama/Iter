## Parent PRD

`ARCHITECTURE.md` §9 Step 1 — Data model; §3 "Tables" (HNSW on `session_embeddings`); §8 "pgvector-migration" trigger (rebuild plan at >10M rows).

## What to build

A one-shot benchmark script that:

1. Generates 10,000 random `vector(N)` rows into `session_embeddings` (N matching whatever dimension is declared in `schema.sql`; pick a single tenant_id for the seed).
2. Records HNSW build time with `m=16, ef_construction=64` (already in `schema.sql`).
3. Issues representative nearest-neighbor queries and records P50/P99 latency.
4. Computes recall@10 against a brute-force baseline (`ORDER BY embedding <-> query LIMIT 10` without the index) on a sample of query vectors.
5. Writes results to a markdown file (e.g. `benchmarks/hnsw-10k-baseline.md`) committed to the repo as the v1 baseline.

The point isn't to tune — it's to confirm the index is actually being used, get a baseline number, and have a reference point for the §8 migration trigger.

## Acceptance criteria

- [ ] Script lives under `scripts/` or `cmd/bench/` and is runnable with a single command
- [ ] 10K rows inserted into `session_embeddings` (cleanup step included or documented)
- [ ] `EXPLAIN ANALYZE` on the nearest-neighbor query shows the HNSW index is used (not seq scan)
- [ ] Build time, P50, P99, and recall@10 recorded
- [ ] Results committed to `benchmarks/hnsw-10k-baseline.md` (or wherever the repo settles on)
- [ ] Script is idempotent (safe to re-run; truncates its own seed data or uses a dedicated tenant)
- [ ] Script does NOT run in CI (too expensive for every PR); document it as on-demand only

## Blocked by

- Blocked by `issues/002-migrations-directory-initial-schema.md`

## User stories addressed

Foundational; supports the `iter suggest` latency-budget user stories (≤1s P99) by establishing baseline retrieval latency.
