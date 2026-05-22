#!/usr/bin/env bash
# scripts/bench-hnsw.sh — HNSW 10K-vector baseline benchmark for `session_embeddings`.
#
# Issue 005: confirm the HNSW index is used and capture a baseline number so
# future scale work has a reference point. Not part of CI — too expensive.
# Run on demand against a dev DB (make db-up) or Railway Postgres.
#
# Idempotent: all rows are scoped to a fixed BENCH_TENANT_ID; the script
# cascade-deletes that tenant on entry. Safe to re-run.
#
# Usage:
#   DATABASE_URL=postgres://... scripts/bench-hnsw.sh [output.md]
#
# Default output: benchmarks/hnsw-10k-baseline.md
#
# Pull the Railway URL with:
#   DATABASE_URL=$(railway variables --service Postgres --environment production \
#       --kv | grep '^DATABASE_PUBLIC_URL=' | cut -d= -f2-)
#
# Prereqs: psql 16+, target DB must already have migration 0001_initial.sql
# applied (so `session_embeddings`, `sessions`, `tenants`, `users`, and the
# HNSW index `idx_embeddings_hnsw` all exist).

set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required (e.g. railway DATABASE_PUBLIC_URL)}"

OUT_FILE="${1:-benchmarks/hnsw-10k-baseline.md}"

# Fixed UUIDs for idempotent re-runs. All bench data lives under this tenant.
BENCH_TENANT_ID="00000000-0000-0000-0000-0000000b3a01"
BENCH_USER_ID="00000000-0000-0000-0000-0000000b3a02"

N_ROWS=10000
DIM=1536
N_QUERIES=100
TOP_K=10
RECALL_SAMPLE=20

PSQL=(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -X --quiet --no-psqlrc)

echo "[bench-hnsw] N_ROWS=$N_ROWS DIM=$DIM N_QUERIES=$N_QUERIES TOP_K=$TOP_K"

# ---------------------------------------------------------------------------
# Capture environment metadata.
# ---------------------------------------------------------------------------
PG_VERSION=$("${PSQL[@]}" -tAc "SELECT version();")
PGVECTOR_VERSION=$("${PSQL[@]}" -tAc "SELECT extversion FROM pg_extension WHERE extname='vector';")
BENCH_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

echo "[bench-hnsw] postgres: $PG_VERSION"
echo "[bench-hnsw] pgvector: $PGVECTOR_VERSION"

# ---------------------------------------------------------------------------
# Clean slate: cascade-delete the bench tenant, then re-create tenant + user.
# ---------------------------------------------------------------------------
echo "[bench-hnsw] cleaning prior bench data (cascade)…"
"${PSQL[@]}" <<SQL
DELETE FROM tenants WHERE id = '${BENCH_TENANT_ID}';
DELETE FROM users   WHERE id = '${BENCH_USER_ID}';

INSERT INTO tenants (id, name, plan) VALUES
  ('${BENCH_TENANT_ID}', 'bench-hnsw-10k', 'free');

INSERT INTO users (id, email, display_name) VALUES
  ('${BENCH_USER_ID}', 'bench-hnsw@iter.local', 'bench-hnsw');
SQL

# ---------------------------------------------------------------------------
# Insert N_ROWS sessions + N_ROWS embeddings under the bench tenant.
# Random vector via array_agg(random())::vector. Single CTE so the wall-clock
# covers row inserts + incremental HNSW maintenance.
# ---------------------------------------------------------------------------
echo "[bench-hnsw] inserting ${N_ROWS} sessions + ${N_ROWS} embeddings…"
INSERT_MS=$("${PSQL[@]}" -tAc "
WITH t0 AS (SELECT clock_timestamp() AS ts),
ins_sessions AS (
  INSERT INTO sessions (
    id, tenant_id, user_id, harness, model, started_at,
    redacted_prompt, classification
  )
  SELECT gen_random_uuid(),
         '${BENCH_TENANT_ID}'::uuid,
         '${BENCH_USER_ID}'::uuid,
         'bench', 'bench-model', now(),
         'bench prompt', 'clean'
  FROM generate_series(1, ${N_ROWS})
  RETURNING id
),
ins_emb AS (
  INSERT INTO session_embeddings (session_id, tenant_id, embedding, embedding_model)
  SELECT s.id,
         '${BENCH_TENANT_ID}'::uuid,
         (SELECT array_agg(random())::vector FROM generate_series(1, ${DIM})),
         'bench-model'
  FROM ins_sessions s
  RETURNING 1
)
SELECT extract(milliseconds FROM (clock_timestamp() - (SELECT ts FROM t0)))::bigint
FROM ins_emb
LIMIT 1;
")
echo "[bench-hnsw] insert (incremental HNSW build) wall: ${INSERT_MS} ms"

ROWS_INSERTED=$("${PSQL[@]}" -tAc \
  "SELECT count(*) FROM session_embeddings WHERE tenant_id='${BENCH_TENANT_ID}';")
echo "[bench-hnsw] rows inserted: ${ROWS_INSERTED}"

# ---------------------------------------------------------------------------
# EXPLAIN ANALYZE: confirm HNSW index is used (not seq scan).
# ---------------------------------------------------------------------------
echo "[bench-hnsw] capturing EXPLAIN ANALYZE…"
EXPLAIN_OUT=$("${PSQL[@]}" -tAc "
WITH q AS (
  SELECT (SELECT array_agg(random())::vector FROM generate_series(1, ${DIM})) AS v
)
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT session_id
FROM session_embeddings
WHERE tenant_id = '${BENCH_TENANT_ID}'
ORDER BY embedding <=> (SELECT v FROM q)
LIMIT ${TOP_K};
")
if echo "$EXPLAIN_OUT" | grep -qi 'idx_embeddings_hnsw'; then
  HNSW_USED="yes"
  echo "[bench-hnsw] HNSW index confirmed in plan"
else
  HNSW_USED="no"
  echo "[bench-hnsw] WARNING: HNSW index NOT seen in plan — review EXPLAIN output"
fi

# ---------------------------------------------------------------------------
# Latency: run N_QUERIES nearest-neighbor queries timed individually inside
# plpgsql, then aggregate P50/P99 with percentile_cont.
#
# Note: psql with -tA returns one column per row; we delimit with '|' and
# parse it back. The DO block doesn't emit a result, so the SELECT below it
# is what comes out of stdout.
# ---------------------------------------------------------------------------
echo "[bench-hnsw] timing ${N_QUERIES} nearest-neighbor queries…"
LAT_RAW=$("${PSQL[@]}" -tAF'|' <<SQL
BEGIN;
CREATE TEMP TABLE bench_lat (i int, ms double precision) ON COMMIT DROP;

DO \$do\$
DECLARE
  i int;
  t0 timestamptz;
  t1 timestamptz;
  qv vector(${DIM});
BEGIN
  FOR i IN 1..${N_QUERIES} LOOP
    SELECT array_agg(random())::vector
      INTO qv
      FROM generate_series(1, ${DIM});

    t0 := clock_timestamp();
    PERFORM session_id
    FROM session_embeddings
    WHERE tenant_id = '${BENCH_TENANT_ID}'
    ORDER BY embedding <=> qv
    LIMIT ${TOP_K};
    t1 := clock_timestamp();

    INSERT INTO bench_lat VALUES (i, extract(milliseconds FROM (t1 - t0)));
  END LOOP;
END
\$do\$;

SELECT
  round(percentile_cont(0.50) WITHIN GROUP (ORDER BY ms)::numeric, 3),
  round(percentile_cont(0.99) WITHIN GROUP (ORDER BY ms)::numeric, 3),
  round(avg(ms)::numeric, 3),
  round(max(ms)::numeric, 3)
FROM bench_lat;
COMMIT;
SQL
)
IFS='|' read -r P50 P99 MEAN MAX <<<"$LAT_RAW"
echo "[bench-hnsw] P50=${P50} ms  P99=${P99} ms  mean=${MEAN} ms  max=${MAX} ms"

# ---------------------------------------------------------------------------
# Recall@K vs brute-force baseline (enable_indexscan = off, enable_bitmapscan
# = off). Averaged over RECALL_SAMPLE query vectors.
# ---------------------------------------------------------------------------
echo "[bench-hnsw] computing recall@${TOP_K} over ${RECALL_SAMPLE} query vectors…"
RECALL_NOTICES=$("${PSQL[@]}" <<SQL 2>&1
DO \$do\$
DECLARE
  i int;
  qv vector(${DIM});
  hits int;
  total_hits int := 0;
  total_possible int := 0;
BEGIN
  FOR i IN 1..${RECALL_SAMPLE} LOOP
    SELECT array_agg(random())::vector
      INTO qv
      FROM generate_series(1, ${DIM});

    DROP TABLE IF EXISTS hnsw_top;
    DROP TABLE IF EXISTS brute_top;

    CREATE TEMP TABLE hnsw_top AS
      SELECT session_id
      FROM session_embeddings
      WHERE tenant_id = '${BENCH_TENANT_ID}'
      ORDER BY embedding <=> qv
      LIMIT ${TOP_K};

    SET LOCAL enable_indexscan = off;
    SET LOCAL enable_bitmapscan = off;
    SET LOCAL enable_indexonlyscan = off;
    CREATE TEMP TABLE brute_top AS
      SELECT session_id
      FROM session_embeddings
      WHERE tenant_id = '${BENCH_TENANT_ID}'
      ORDER BY embedding <=> qv
      LIMIT ${TOP_K};
    SET LOCAL enable_indexscan = on;
    SET LOCAL enable_bitmapscan = on;
    SET LOCAL enable_indexonlyscan = on;

    SELECT count(*) INTO hits
      FROM hnsw_top h JOIN brute_top b USING (session_id);

    total_hits := total_hits + hits;
    total_possible := total_possible + ${TOP_K};
  END LOOP;

  RAISE NOTICE 'RECALL_VALUE %', round((total_hits::numeric / total_possible::numeric), 4);
END
\$do\$;
SQL
)
RECALL=$(echo "$RECALL_NOTICES" | grep -oE 'RECALL_VALUE [0-9.]+' | awk '{print $2}' | head -1)
echo "[bench-hnsw] recall@${TOP_K} = ${RECALL}"

# ---------------------------------------------------------------------------
# Emit markdown report.
# ---------------------------------------------------------------------------
mkdir -p "$(dirname "$OUT_FILE")"

{
  echo "# HNSW 10K baseline — \`session_embeddings\`"
  echo
  echo "_Generated by \`scripts/bench-hnsw.sh\` on ${BENCH_DATE}._"
  echo
  echo "## Environment"
  echo
  echo "| Field | Value |"
  echo "|---|---|"
  echo "| Postgres | \`${PG_VERSION}\` |"
  echo "| pgvector | \`${PGVECTOR_VERSION}\` |"
  echo "| Dimension | ${DIM} |"
  echo "| Rows inserted | ${ROWS_INSERTED} |"
  echo "| Index params | \`hnsw (vector_cosine_ops) m=16, ef_construction=64\` |"
  echo "| Distance op | \`<=>\` (cosine) |"
  echo "| Top-K | ${TOP_K} |"
  echo "| Query sample (latency) | ${N_QUERIES} |"
  echo "| Query sample (recall) | ${RECALL_SAMPLE} |"
  echo "| HNSW used in plan | ${HNSW_USED} |"
  echo
  echo "## Results"
  echo
  echo "| Metric | Value |"
  echo "|---|---|"
  echo "| Insert + incremental HNSW build (wall) | ${INSERT_MS} ms |"
  echo "| Query P50 | ${P50} ms |"
  echo "| Query P99 | ${P99} ms |"
  echo "| Query mean | ${MEAN} ms |"
  echo "| Query max | ${MAX} ms |"
  echo "| Recall@${TOP_K} vs brute-force | ${RECALL} |"
  echo
  echo "## EXPLAIN ANALYZE (ANN query)"
  echo
  echo '```'
  echo "$EXPLAIN_OUT"
  echo '```'
  echo
  echo "## Notes"
  echo
  echo "- Vectors are uniform random in \`[0,1)\` and **not** normalized. Real-world"
  echo "  embeddings have a tighter angular distribution, so production recall at"
  echo "  the same HNSW parameters will typically be *higher*. Treat these numbers"
  echo "  as a floor, not a target."
  echo "- The script seeds under a dedicated bench tenant"
  echo "  (\`${BENCH_TENANT_ID}\`) and cascade-deletes that tenant on every run."
  echo "  Safe to re-run; safe to leave the bench tenant in the DB after the run"
  echo "  — separate UUID, separate RLS scope, no pollution of real tenants."
  echo "- Per \`ARCHITECTURE.md\` §8, the pgvector-migration trigger is row count"
  echo "  >10M. This 10K baseline is a sanity check that HNSW is wired up and a"
  echo "  reference for the next order-of-magnitude milestone — not a tuning run."
  echo "- **Not run in CI** — too expensive for every PR. On-demand only."
} > "$OUT_FILE"

echo "[bench-hnsw] wrote $OUT_FILE"
