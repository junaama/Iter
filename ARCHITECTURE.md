# Iter — Architecture (v1)

Iter is a Mac app for teams using coding agents. It wraps native terminal harnesses (Claude Code, Codex, Pi, OpenCode, Gemini CLI), captures agent traces locally, strips secrets before any cloud sync, scores outcomes, and surfaces prompt refinements at task-start time. A teammate's learnings show up in your next session as a better prompt suggestion. Distributed via iter.dev. Agent-led queries powered by CLI + skill.md.

This document is the source of truth for what Iter v1 is. When the implementation drifts from this doc, update the doc.

## 1. Requirements

### Core user actions
1. Download Mac app from iter.dev.
2. Invite teammates to a tenant.
3. Set up the harnesses inside the Iter environment (Claude Code, Codex, Pi, OpenCode, Gemini CLI).
4. Work as normal. See better prompt refinements over time as the system learns from team activity.
5. See a dashboard of what is working across the user and the team.

### Canonical user journey
Adam is an engineer at a 50-person startup. He installs Iter, hooks into his native environment. Iter runs a background job to ingest his recent sessions as traces. Adam sees a score of his past sessions and continues to use his preferred coding agent harness. Adam writes the prompt `@BusinessOwnerDashboardTable.tsx Migrate this table to our new design-system.`. Iter intercepts the prompt and offers a refinement: attach design-system context so the agent does not spend time looking for it. The refined prompt becomes `@BusinessOwnerDashboardTable.tsx @design-system/new-dashboard/table Migrate the style used in the table to the new table design system.`. Adam uses the refined prompt. Adam invites Ben. Ben joins the team. Iter ingests Ben's past sessions. Ben now sees results like `You wrapped up Linear Ticket Dev-Bug-68 (XL) in 3 turns and 40 minutes of wall time by parallelizing across 4 agents.`

### Data in / out / persisted
- **In**: user prompt, agent session details, custom system prompts, tool calls, MCP servers used, wall time, subagent prompts, turn counts, git blame, effort tier, tool modes (`chrome`, `browser_use`, `computer_use`).
- **Out (to cloud)**: trufflehog-stripped JSON; never raw source code, never secrets. Aggregations and scoring results returned to the dashboard.
- **Persisted**: classified session records, scored summaries (kept indefinitely), full traces (90 days hot in Postgres, archive to Cloudflare R2 thereafter).

### Users
Engineers and non-engineers who use coding agents for any digital task ("update this macro from Excel," "refactor this codebase to Rust" both valid). Lightweight DevOps admin role at the team level. Primary buyer is the team; solo developers get a strictly local learning pool as a free seeding tier.

### Integrations at v1
Harnesses (no formal connectors required, pattern matching against on-disk session files): Claude Code, Codex, Pi, OpenCode, Gemini CLI. The Mac app requests the macOS permissions needed for file watching and accessibility. Webhooks: GitHub and Linear at v1.

### Non-functional
- Read/write ratio: roughly 40:60.
- `iter suggest` latency: ≤1s P99 end-to-end.
- Capture write path: async.
- Scoring: nightly batch + on-demand refresh, cache-preferred.
- Availability: 99.0% target.
- Consistency: eventual; exact on refresh.
- Tenant isolation: enforced at the database via Row-Level Security on every tenant-scoped table.

### Scale targets
- v1 (bootcamp + first paid): 30 engineers, 2–3 teams.
- Month 1: 500 engineers, 20 teams.
- Month 3: 1,500–5,000 engineers.
- Month 6: 10,000–25,000 engineers.

Architecture is sized for the month-3 (5K) ceiling with a documented migration path to 25K.

## 2. Capacity math

At the 5K engineer ceiling with 50 sessions/user/day and 10 outcome events/session:

| Quantity | Value |
|---|---|
| Sessions / day | 150,000 |
| Outcome events / day | 1,500,000 (~17/sec avg, ~50–90/sec peak) |
| Storage / session (compressed, redacted) | ~21 KB |
| Storage / day | ~3.15 GB |
| Storage / month | ~95 GB |
| Storage / year (no retention) | ~1.1 TB |
| Embedding rows / day | 150,000 (vector(1536), ~6 KB each) |

### LLM costs at v1 scale
With cheap-tier models (Haiku / Gemini Flash / Qwen) for the hot path and Sonnet-tier only where quality demands:
- Suggestions: ~75K calls/day at ~$0.0015/call (cheap tier with caching) ≈ $112/day.
- Scoring (nightly): ~150K traces/day at ~$0.005/call (cheap tier) ≈ $750/day.
- Embeddings: ~$60/day.

Total LLM spend at month 3: ~$925/day. Subsidized by startup credits and operating-at-loss in v1. Multi-provider routing in place so the cheapest provider per workload wins.

### Latency budget for `iter suggest` (≤1s P99)
| Step | Budget |
|---|---|
| Network RTT | ~50ms |
| Auth + tenant context (Redis) | ~10ms |
| Embed user prompt | ~100ms |
| pgvector ANN search | ~50ms |
| Fetch top-K traces | ~30ms |
| LLM call (cheap tier, ~3K in, ~200 out) | ~250–400ms |
| Format response | ~10ms |
| **Total estimated** | **~500–650ms** |
| Slack | **~350–500ms** |

## 3. Data layer

### Storage
Single store at v1: PostgreSQL 16+ with `pgvector`, `pgcrypto`, `citext` extensions. Redis for cache + Streams as the durable queue.

### Tables
- `tenants`, `users`, `tenant_users` — tenancy and membership.
- `sessions` — one row per agent session, including subagents (self-referential `parent_session_id` FK). Carries `effort` and `tools[]`.
- `session_events` — append-only lifecycle and outcome log. Replay/backfill source of truth.
- `session_embeddings` — `vector(1536)` per session. HNSW index.
- `session_scores` — one or more scoring runs per session. Carries `composite_score`, `signals` (JSONB), `contributor_weight`, `rationale`.
- `outcomes` — links sessions to downstream git / incident events.
- `suggestions` — cached prompt refinements, embedded for similarity lookup.
- `stacks` — lightweight shareable stacks (harnesses, skills, docs references, notes). NOT raw configs or env values.
- `stack_shares` — share grants, per team or per user.
- `archive_pointers` — Cloudflare R2 object URI pointers for sessions older than 90 days.

### Tenant isolation
RLS enabled on every tenant-scoped table. Per-transaction `SET LOCAL app.current_tenant = '<uuid>'`. Privileged `iter_batch` role uses `BYPASSRLS` for nightly scoring and archive jobs only; never reachable from the request path.

### Stacks
A stack captures the *wrapped solution* a user is running: which harnesses, which skills, which doc references, free-form notes. It does not capture configs, env vars, secrets, MCP server credentials, or raw files. On share, the user can deselect any included file reference. Sharing is per team (whole tenant team) or per individual teammate; never cross-tenant.

### Pre-ingestion redaction
Every payload from the daemon is scanned by trufflehog (or vendored detectors) before it touches Postgres. Records are classified as `clean`, `strippable`, or `dirty`. Only `clean` and successfully redacted `strippable` records reach the cloud. `dirty` records remain on the user's device and contribute to local-only scoring.

### Retention
Hot in Postgres for 90 days. After 90 days, full payload moves to Cloudflare R2 with an `archive_pointers` row pointing to the manifest. Scored summaries remain in Postgres indefinitely. R2 is chosen over AWS S3 for zero egress fees (matters for tenant data exports and the post-ingestion-leak cascade-purge path); S3-compatible API means the AWS SDK is reused with a custom endpoint.

### Documented migration triggers (5K → 25K)
| Trigger | Action |
|---|---|
| >1K concurrent WebSocket connections sustained | Horizontally scale the Go gateway behind a sticky LB; same binary. |
| >10M rows in `session_embeddings` (or HNSW rebuild pain) | Move vectors to Pinecone or Turbopuffer; keep metadata in Postgres. |
| Sustained write QPS >2K | Read replicas; partition `session_events` by `tenant_id` + month. |
| `iter suggest` P99 >800ms | Split the suggest API into its own service for independent scaling. |
| Nightly scoring runtime >6 hours | Shard the batch by tenant; per-tenant scoring workers. |

## 4. Compute layer

### Topology
- **One Go binary** running the WebSocket gateway, `iter suggest` API, webhook receivers, ingestion processor, embedding worker, dashboard API, and archive cron. Hosted on Railway.
- **PostgreSQL** managed (Railway or Neon).
- **Redis** managed; used both as cache and as Redis Streams durable queue.
- **Modal** for nightly scoring batch (GPU-ready for future fine-tuned scorer).
- **WorkOS** for auth (device-code flow for CLI/daemon, SSO/SAML path for enterprise).

### Workloads in the one Go binary
| Workload | Shape |
|---|---|
| WebSocket gateway | Persistent connections, sticky routing once horizontally scaled. ~3K–5K concurrent at month-3. |
| `iter suggest` | Synchronous HTTP, ≤1s P99. |
| Ingestion processor | Goroutine pool consuming from in-memory queue + Redis Streams durability backstop. |
| Embedding worker | Batch puller from Redis Stream every 30s; uses Voyage/OpenAI batch APIs. |
| Webhook receivers | HMAC-verified inbound from GitHub and Linear. |
| Archive cron | Daily, 03:00 UTC. Moves >90-day records to Cloudflare R2, writes `archive_pointers`. Runs the R2 free-tier guardrail (§7) before every `PutObject`. |
| Dashboard API | Read-heavy; shares the suggest path's connection pools. |

### Daemon resilience
The Mac daemon writes traces to a local SQLite WAL before sending. On WS reconnect, it replays unsent records. Most processing happens locally — losing a single trace is not catastrophic, but the WAL means it rarely happens at all.

### Failure modes
| Failure | Behavior |
|---|---|
| WS gateway crash | Daemon buffers to SQLite WAL; replays on reconnect. |
| Postgres unavailable | `iter suggest` falls back to Redis-cached suggestions. Capture buffers in Redis Streams (TTL 24h). |
| LLM provider unavailable | `iter suggest` returns `no_suggestion_reason: llm_unavailable`. Caller treats as "use original prompt." Never blocks. |
| Post-ingestion leak detected (improved classifier) | Delete-cascade the affected `session_id` everywhere. Write audit-log entry. |

## 5. API surface

### Wire formats
- Daemon ↔ cloud: WebSocket, JSON message envelope, message-typed discriminator on `type`. Bidirectional. Server may push `suggestion.available` or `suggestion.preempt`.
- CLI ↔ cloud: REST/JSON.
- Dashboard ↔ cloud: REST/JSON. Shares endpoints with CLI where applicable.
- Webhooks: inbound REST/JSON, HMAC-verified.

No GraphQL, no gRPC, no SSE streaming, no public API, no NL search across sessions at v1.

### REST endpoints

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/suggest` | Latency-critical prompt refinement. |
| GET | `/v1/stack/me` | Return the caller's saved stack plus share grants; empty/404 means render detected draft locally until explicit save. |
| GET | `/v1/stack/:user` | Return a teammate's shared stack (auth + share check). |
| POST | `/v1/stack` | Upsert the caller's stack. |
| POST | `/v1/stack/:id/share` | Share a stack with team or specific user; request may include the selected file references to share. |
| GET | `/v1/sessions/:id` | Fetch one session (audit/debug). |
| GET | `/v1/sessions?filter=...` | Search/filter sessions visible to the caller. |
| GET | `/v1/scores/:session_id` | Score detail + rationale. |
| GET | `/v1/dashboard/me` | Personal dashboard data. |
| GET | `/v1/dashboard/team` | Team aggregates. |
| POST | `/v1/webhooks/github` | GitHub HMAC-verified webhook. |
| POST | `/v1/webhooks/linear` | Linear signing-secret-verified webhook. |

### `POST /v1/suggest` contract
Request includes `session_context` with `harness`, `model`, `effort` (one of `low|med|high|xhigh|max`), `tools[]` (e.g. `chrome`, `browser_use`, `computer_use`), `repo_hash`, `cwd_files[]`, `raw_prompt`. Response includes `refined_prompt`, `rationale`, `confidence` (0.0–1.0), optional `evidence[]`, and `no_suggestion_reason` when the system declines.

### Confidence thresholds (locked)
- Confidence < 0.50: suppress. Do not surface.
- Confidence 0.50–0.80: advisory. Surface as a suggestion the user can apply.
- Confidence ≥ 0.80: replace. Surface as a clipboard-ready replacement; never inject into a terminal directly.

Clients call a pure decision function (`suggestion_action(confidence, refined_prompt)`) to decide UI behavior. The thresholds live in one place: `contracts.py`.

### Auth
WorkOS-issued bearer tokens. Tokens carry `tenant_id` claim. Stored in macOS Keychain on the user's machine under service `dev.iter.IterApp` with accounts `access_token`, `refresh_token`, and `id_token`; refresh tokens use device-only after-first-unlock accessibility. Device-code OAuth flow on `iter login` and first app launch; refresh starts 60s before expiry and 401 responses retry once after refresh. SSO/SAML available via WorkOS when an enterprise needs it.

### Versioning + idempotency + rate limits
- Versioning prefix: `/v1/`. When `/v2/` ships, both run for at least 6 months.
- Idempotency: `Idempotency-Key` header accepted on all POST endpoints; required for webhook inbound.
- Rate limits per token: 100 req/min CLI, 600 req/min daemon.

### Account lifecycle API
- `POST /v1/account/export` creates a tenant-scoped export record for the signed-in user and returns a polling URL. `GET /v1/account/export/{id}` returns `pending`, `ready`, or `failed` plus a download URL or archive pointer when ready. v1 records a durable archive pointer; R2 bundle generation can be layered behind that pointer without changing the Settings contract.
- `POST /v1/account/delete` soft-disables the signed-in user and records a 7-day cascade-delete schedule for the current tenant. Both POST endpoints require `Idempotency-Key`, authenticated tenant context, and audit entries without raw payloads.

## 6. UI/UX

### Shell
SwiftUI native Mac app. Native SwiftUI components, no third-party UI library. Server state via URLSession + Observation framework; daemon ↔ Mac app via Unix domain socket.

### Screen inventory
1. **Onboarding wizard** — sign in (WorkOS device code) → grant macOS permissions (Accessibility + Full Disk Access scoped to harness dirs) → tenant confirmation. Initial ingest runs in the background, not as a wizard blocker. Tenant confirmation happens after permissions are granted. If the user's email domain matches an existing tenant, the UI offers "Your team's on Iter! Join / Skip"; on Join, the request requires admin approval and the user accepts the join (three-party handshake).
2. **Menubar dropdown** — always-visible status: current ingest activity, last session captured, "Open Dashboard," "Share my stack," pause-capture toggle. Polls the local daemon over the Unix socket every 5s.
3. **Dashboard — Me** — rolling 7d/30d avg score, trend sparkline, recent sessions list, score detail on click, contributor weight indicator.
4. **Dashboard — Team** — team member aggregates, "top patterns this week" panel, invite button visible only to team admins.
5. **Session detail** — redacted prompt, harness/model/effort/tools, wall time, turn count, score breakdown by signal, attached outcomes, subagent tree (collapsed by default).
6. **Stack — Me** — harnesses chips, skills list, doc references, notes textarea, save button, share grants list.
7. **Stack — Simulate teammate** — read-only pills showing the teammate's stack composition. "Use stack in directory" opens a new git worktree in a chosen directory as a simulation sandbox; this is not a runtime environment switch.
8. **Settings** — account, tenant, capture toggles per harness, retention info (read-only), redaction rules preview, data export, account deletion, notification toggle.
9. **(Transient) Suggestion popover** — native macOS notification with actions "Copy to clipboard," "Dismiss," "Suppress this pattern." Auto-dismiss after 8s. Never injects into a terminal directly; clipboard only. Clicking the notification opens a tiny native panel with rationale and evidence.

### Anti-screens (out of v1)
No NL search across team sessions. No public profile pages. No leaderboard as a primary feature. No in-app editing of CLAUDE.md / AGENTS.md / skill.md. No in-app billing or tenant admin (separate web admin on iter.dev).

### Real-time behavior
- Suggestion popover: `/v1/suggest` produces a `suggestion.available` cloud WebSocket push; the daemon applies the shared decision function, suppresses deny-list hits, queues the notice, and exposes it to the Mac app through the Unix-socket `suggestion.available` IPC method. No SSE.
- Menubar status: polled from local daemon every 5s.
- Dashboard: fetch on mount, refetch on focus, manual refresh button.
- Daemon ↔ cloud trace ingest: real-time via WebSocket; user does not see this directly.

### State management
- Local state: per-view UI state, in-component.
- Server state: from `/v1/*` endpoints, cached per-endpoint TTL (suggest = no cache, dashboard = 30s, stack = 60s, sessions = on-event invalidation).
- Daemon state: in the Go daemon, exposed via Unix socket.
- URL-driven state: which dashboard tab, which session detail page, which stack profile.

### Default-state handling
- Empty dashboard (new user, no scored sessions): show "first scored session estimated in <X> hours," not "no data."
- Empty team (solo dev): one-row table with "you" and "invite teammate" CTA.
- Stack not yet captured: stack/me shows detected current setup as a draft, save explicit.
- Inherited team defaults: show the inherited value with its source, never implicitly.

## 7. Reliability

### Failure modes and mitigations
| Failure | Mitigation |
|---|---|
| WS gateway crash | Daemon SQLite WAL; replay on reconnect. |
| Postgres unavailable | `iter suggest` falls back to Redis-cached suggestions. Capture buffers in Redis Streams (24h TTL). |
| LLM provider unavailable | Multi-provider routing; return `no_suggestion_reason: llm_unavailable` if all exhausted. Never blocks. |
| Post-ingestion leak detected | Delete-cascade affected `session_id` everywhere; write audit-log entry. |
| Daemon crashes | launchd auto-restart; WAL preserves unsent traces. |
| Mac disk full | Daemon WAL capped at 500 MB; oldest unsent records evicted with audit warning; capture continues. |
| Modal scoring job fails | Retry once; on second failure, fall back to stale scores + alert. Sessions remain capturable. |
| Embedding provider unavailable | Queue with backoff; session viewable but not searchable until embedding lands. |
| Trufflehog detector update produces false positives | Reclassify on next ingest cycle; no data loss. |
| WorkOS auth provider down | Existing tokens valid until expiry; new logins fail with clear error. Token TTL = 24h refresh, 7d max. |
| Noisy-neighbor tenant | Per-tenant rate limit at the gateway; p99 capped at 10x median. |
| Webhook delivery delayed | Best-effort; outcomes attach when they arrive. Idempotency-Key dedup. |
| Scoring bug at scale | `scorer_version` column enables rollback; UI shows "scored by vN." |
| Database corruption | Postgres PITR is the v1.x target (max acceptable loss = 1h); **deferred at v1** because PITR requires the Railway Pro plan and v1 runs on Hobby. Until upgrade, fallback is the daily snapshot Railway retains on Hobby (accept ~24h loss). Re-enable + re-evaluate the 1h target when scaling. See `issues/deferred/006-pitr-backup-restore-drill.md`. |
| Mac app / daemon version drift | Daemon refuses to start on major-version mismatch; prompts update. |
| macOS permissions revoked mid-session | Daemon detects, pauses capture, surfaces in menubar. |
| Tenant deleted (admin action) | Cascade-delete; R2 archives purged within 24h; audit log entry. |
| R2 free-tier ceiling approached | Alert at 80% of storage / Class A / Class B ceilings; archive cron refuses new writes at 95% unless `R2_OVERAGE_OK=true`. Details in `deploy.md` "R2 usage monitoring." |
| User account deleted | `POST /v1/account/delete` soft-disables the signed-in user, writes `data_deletion_requested`, then follows the 7-day cascade-delete path at user scope. Confirmation required. |
| Suggestion contains harmful pattern (`rm -rf`, `DROP TABLE`, `git push --force`) | Deny-list filter blocks; log security event; never surface. |
| pgvector recall degrades | Weekly recall check on held-out set per tenant; rebuild HNSW when recall < 0.85. |

### Patterns in use
Circuit breaker on every external call (LLM, embedding, WorkOS, webhook outbound). Retry + exp backoff + jitter, capped at 3–5 retries. `/health` endpoint checks DB + Redis + LLM routes. Idempotency keys on all POST endpoints. Feature flags via Postgres `feature_flags` table + Redis cache. Dead-letter queues via Redis Streams `dlq:*`.

### Observability
- Sentry for app errors. BetterStack for logs, metrics, uptime, status page, on-call.
- Self-hosted Langfuse on Railway for LLM and agent traces.
- Email notifications only at v1.
- Minimum alerts: `iter suggest` P99 > 1.5s for 5m, error rate > 1% for 5m, scoring batch fails, Postgres connection exhaustion, WS connection count over threshold, trufflehog scan failure rate > 0.1%, **R2 free-tier usage ≥80% on any of {storage, Class A ops, Class B ops}, R2 egress >2× 7-day rolling baseline**.

### Incident response
Solo engineer at v1. Email alerts only. status.iter.dev published via BetterStack. SLO: 1h ack for P1, 4h for P2, next-business-day for P3. Runbooks live in the repo, one per top-5 alert.

## 8. Scaling story

### Architecture sized for 5K engineers; documented migration path to 25K
Beyond 25K, the architecture is redesigned, not extended.

### Monitored migration triggers
| Trigger | Metric | Threshold |
|---|---|---|
| Horizontal WS gateway | Concurrent connections per instance | >1K sustained 24h |
| Vector DB migration (Pinecone/Turbopuffer) | `session_embeddings` row count + HNSW rebuild time | >10M rows OR rebuild >30m |
| DB read replicas + partitioning | Write QPS + p99 commit latency | >2K QPS OR p99 >50ms |
| Split suggest API | `iter suggest` P99 | >800ms for 24h |
| Per-tenant scoring shards | Nightly batch wall time | >6h |

All five monitored in BetterStack from v1 launch.

### Pre-budgeted constraints
- pgvector HNSW rebuild memory at the 10M-row threshold requires a one-time large-RAM Postgres instance for the final rebuild before migration to Pinecone/Turbopuffer.
- Modal warm pool N=2 workers always running; spin beyond on demand.
- Railway WebSocket production-readiness verified before gateway code is written; fallback is to host the gateway on Fly.io.

### Architectural progression
| At ~5K | At ~10K | At ~25K |
|---|---|---|
| Single Go binary | Same binary, multiple replicas behind sticky LB | Possibly split: gateway / API / batch coordinator |
| Postgres + pgvector | Postgres + Pinecone; metadata in PG | Postgres with read replicas + Pinecone; partitioned `session_events` |
| Redis single instance | Redis cluster mode | Redis cluster + dedicated stream broker if needed |
| Modal for scoring | Modal sharded by tenant | Modal + spot GPU pool; in-house fine-tuned scorer considered |
| One region (US-East) | Multi-region read replicas | Multi-region active-active; data residency negotiable |
| Solo engineer | Two engineers minimum | Real on-call rotation, SRE practices |

## 9. Build order

The build sequence. Each item is small enough to be a single PR or one focused session.

### Step 1 — Data model
Provision Postgres 16+ on Railway. Verify pgvector, pgcrypto, citext extensions. Run `schema.sql`. Create `iter_batch` role; verify BYPASSRLS. Set up `migrations/` directory starting at `0001_initial.sql`. Insert sample rows; verify cascade-delete and RLS isolation per table. Build HNSW on 10K random vectors; record build time and recall. ~~Verify PITR backup + restore procedure.~~ (PITR drill deferred until Railway Pro plan upgrade — see `issues/deferred/006-pitr-backup-restore-drill.md` and §7 row "Database corruption.")

### Step 2 — Tests for business logic
Pure scoring function with property and table-driven tests. Suggestion decision function with threshold-boundary tests. Classification function (trufflehog wrapper) with secrets corpus + PII corpus + idempotency + determinism tests. Signal aggregation tests (ordering independence, idempotency on duplicate events). Dangerous-pattern deny-list tests.

### Step 3 — Storage layer + ops setup
Repository structure: one Go module, `cmd/server`, `internal/{db,api,ws,ingest,scoring,redact,auth}`, `pkg/contracts`. Postgres connection layer with `pgxpool` + PgBouncer transaction mode; helper to `SET LOCAL app.current_tenant`. Redis client + stream consumer groups + DLQ naming convention. Repository functions per table; testcontainers-backed tests, not mocks. Embedding provider abstraction with circuit breaker + Redis cache by SHA256. LLM provider abstraction with routing + circuit breaker + fallback chain. Railway project (dev/staging/prod); Postgres + Redis provisioned; secrets in Railway env vars. Modal account + stub function. WorkOS OIDC app. GitHub repo with branch protection and CI (`go test` + `golangci-lint`). Railway CD: auto-deploy main → staging, manual promotion → prod. BetterStack uptime monitor on staging `/health`. Domain DNS for iter.dev + staging.iter.dev.

### Step 4 — API endpoints
HTTP server skeleton (chi or gin, lock one). Middleware stack: request ID → logger → auth → tenant context → rate limit → idempotency → handler. `/health` returns ok + db + redis + llm_routes. Auth middleware (WorkOS JWT, JWKS cache 1h). Rate limit middleware (per-token sliding window in Redis). Idempotency middleware (24h cached responses). Handlers in this order: `POST /v1/suggest` (full pipeline) → `GET /v1/dashboard/me` → `GET /v1/sessions/:id` + `GET /v1/scores/:session_id` → stacks endpoints → `GET /v1/dashboard/team` → `GET /v1/sessions?filter=` → GitHub + Linear webhooks. WebSocket gateway: discriminated message router, ack every message, per-connection goroutines, heartbeat ping 30s. Daemon ingestion pipeline (file watchers + SQLite WAL + trufflehog scan + WS publish + server-side Redis Stream → Postgres → embedding enqueue). Embedding worker (batch from `embed:queue`, retry with backoff, DLQ after 5 failures). Modal scoring batch at 02:00 UTC (idempotent, version-tagged). Archive cron at 03:00 UTC (90-day cutoff, R2 upload via S3-compatible client + free-tier guardrail, archive_pointers row, batched deletes).

### Step 5 — Errors, edge cases
`iter suggest` failure modes: provider 5xx → fallback chain; budget exceeded → cancel; no nearby past → `no_evidence`; all candidates < 0.5 → `low_confidence`; deny-list hit → silent (do not signal which pattern was caught). Daemon edge cases: WAL cap eviction; permissions revocation detection; Mac sleep/wake reconnection; version mismatch. Ingestion edge cases: replay (upsert), out-of-order events, corrupted session file (log + skip), trufflehog failure (fail-closed). Scoring edge cases: zero sessions in 24h (skip), single session with no peer reuse (low-confidence flag), subagent independent scoring, archive collision (skip recent-modified). Webhook edge cases: orphan webhooks → pending-outcomes retry 7 days, duplicates → no-op, bad signature → 401 + audit. Auth edge cases: expired token, deleted user, tenant mismatch. Data lifecycle: account deletion soft-disable then 7-day cascade, tenant deletion immediate cascade, former teammate display name "former member."

### Step 6 — Tests (unit + integration + e2e)
Unit: 80% line coverage on `internal/`. Integration: testcontainers Postgres + Redis; repository functions, cascade deletes, RLS isolation per table, WS round-trip + ack + reconnect, embedding worker, LLM routing, idempotency, rate limits. E2E: full Adam journey as a single test script; login, ingest, suggest, webhook, archive; runs against staging. Load: WS gateway 5K concurrent for 10m, suggest 50 QPS for 30m, embedding 10K queued. Security: cross-tenant access, token replay, deny-list bypass variants, trufflehog regression fixtures.

### Step 7 — Observability
Structured logs (JSON to stdout, BetterStack). Prometheus metrics: HTTP per-endpoint, WS connections + messages, pgx pool stats, Redis stream depth, LLM calls + latency + cost, scoring batch metrics, embedding queue metrics. Langfuse self-hosted on Railway for LLM traces. BetterStack alerts (six minimum from §7). status.iter.dev published. Runbooks: postgres-down, llm-provider-down, scoring-batch-failed, ws-connection-storm, post-ingestion-leak, pgvector-migration skeleton.

### Step 8 — UI polish
SwiftUI app: Xcode project, codesign + notarize, launchd plist for daemon, menubar item, light/dark theming. Onboarding wizard (sign in + permissions + tenant confirm; ingest background). Menubar (status icon + dropdown + Unix-socket poll). Dashboard - Me (header + trend + recent sessions). Dashboard - Team (member table + top patterns + admin-only invite). Session detail (redacted prompt + score breakdown + tool tree). Stack screens (pills + lists + worktree picker for simulation). Suggestion notification (UserNotifications framework, Copy to clipboard / Dismiss / Suppress, 8s auto-dismiss). Settings. All screens have explicit loading / error / empty / inherited-default states.
