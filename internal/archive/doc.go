// Package archive owns the 90-day-cutoff cron that moves sessions from
// Postgres to Cloudflare R2 (ARCHITECTURE.md §3 Retention + §9 Step 4).
//
// Boundaries:
//
//   - `internal/archive` only ever talks to Postgres via the BYPASSRLS
//     `WithBatch` helper. It MUST NOT depend on the request-path
//     `WithTenant` pool — running the archive under per-tenant RLS would
//     require N separate scans per night, defeating the whole point of
//     batched cross-tenant work.
//
//   - The R2 client is an interface (`ObjectStore`) so the integration
//     test can stub it without spinning up a real bucket. cmd/server
//     instantiates the AWS-SDK-backed implementation; tests pass an
//     in-memory stub. This is the same shape DECISIONS.md uses for the
//     LLM and embedding routers.
//
//   - The free-tier guardrail (`UsageMeter`) is also an interface so
//     tests can deterministically push the usage above 80% without
//     mocking the Cloudflare Analytics API HTTP shape.
//
// Cron ownership: ARCHITECTURE.md §4 enumerates two scheduled jobs at
// v1 — Modal nightly scoring (02:00 UTC) and the archive cron (03:00
// UTC). Both share the BYPASSRLS pool but are otherwise independent;
// they live in different binaries (Modal in `modal/`, archive here)
// because Modal's GPU-ready warm pool is wasted on a pure DB+R2 sweep.
package archive
