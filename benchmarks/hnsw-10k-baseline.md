# HNSW 10K baseline — `session_embeddings`

> **Status:** baseline numbers not yet populated. The bench script
> (`scripts/bench-hnsw.sh`) is in place; the operator has to run it once
> against the chosen target (local `make db-up` Postgres or Railway
> Postgres) to fill in the table below. This file is committed as the
> v1 reference document the script overwrites on each run.

## How to run

```sh
# Option A: local pgvector via docker (free, repeatable)
make db-up
make migrate-up
DATABASE_URL=postgres://iter:iter@localhost:5433/iter?sslmode=disable \
  ./scripts/bench-hnsw.sh

# Option B: Railway Postgres (production hardware baseline)
DATABASE_URL=$(railway variables --service Postgres --environment production \
    --kv | grep '^DATABASE_PUBLIC_URL=' | cut -d= -f2-) \
  ./scripts/bench-hnsw.sh

# or via Makefile target
make bench-hnsw
```

The script overwrites this file with the latest results.

## Environment

| Field | Value |
|---|---|
| Postgres | _filled by script_ |
| pgvector | _filled by script_ |
| Dimension | 1536 |
| Rows inserted | 10000 |
| Index params | `hnsw (vector_cosine_ops) m=16, ef_construction=64` |
| Distance op | `<=>` (cosine) |
| Top-K | 10 |
| Query sample (latency) | 100 |
| Query sample (recall) | 20 |
| HNSW used in plan | _filled by script_ |

## Results

| Metric | Value |
|---|---|
| Insert + incremental HNSW build (wall) | _filled by script_ |
| Query P50 | _filled by script_ |
| Query P99 | _filled by script_ |
| Query mean | _filled by script_ |
| Query max | _filled by script_ |
| Recall@10 vs brute-force | _filled by script_ |

## EXPLAIN ANALYZE (ANN query)

_Filled by script — must show `idx_embeddings_hnsw` (not `Seq Scan`)._

## Notes

- Vectors are uniform random in `[0,1)` and **not** normalized. Real-world
  embeddings have a tighter angular distribution, so production recall at
  the same HNSW parameters will typically be *higher*. Treat the recall
  number as a floor, not a target.
- The script seeds under a dedicated bench tenant
  (`00000000-0000-0000-0000-0000000b3a01`) and cascade-deletes that tenant
  on every run. Safe to re-run; safe to leave the bench tenant in the DB
  afterwards — separate UUID, separate RLS scope, no pollution of real
  tenants.
- Per `ARCHITECTURE.md` §8, the pgvector-migration trigger is row count
  >10M. This 10K baseline is a sanity check that HNSW is wired up and a
  reference for the next order-of-magnitude milestone — not a tuning run.
- **Not run in CI** — too expensive for every PR. On-demand only.
