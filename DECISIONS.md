# Iter — Decision Log

The complete decision log from the system design session. Every line is a binding decision; when one of these changes, this file and the corresponding artifact change together.

## Phase 1: Requirements
- **Product**: Iter, distributed at iter.dev. Mac app for teams using coding agents.
- **Distribution shape**: daemon + menubar dashboard + CLI binary (`iter suggest`, etc.). Not a terminal replacement.
- **Prompt-refinement trigger**: mix of (a) skill.md-instructed agent calls to `iter suggest` and (c) post-prompt-send monitoring with user-facing suggestion. Shell wrapper deferred.
- **Primary buyer**: team (eng/ops manager, 20–200 person company). Solo = free seed tier; gets self-only learning pool.
- **Capture inputs**: user prompt, agent session details, custom system prompts, tool calls, MCPs used, wall time, subagent prompts, turns, git blame, effort tier, tool modes (chrome, browser_use, computer_use).
- **Outputs to cloud**: trufflehog-stripped JSON logs; raw source code never syncs.
- **Supported harnesses at v1**: Claude Code, Codex, Pi, OpenCode, Gemini CLI. Pattern matching on session files; no formal integrations required.
- **Latency budget for `iter suggest`**: ≤1s P99. Capture write path async. Scoring async, batched nightly + on-demand refresh, cache-preferred.
- **Availability target**: 99.0%.
- **Consistency**: eventual; exact on refresh.
- **Read/write ratio**: 40:60.
- **Scale targets**: 30 (v1) / 500 (M1) / 1,500–5,000 (M3) / 10K–25K (M6) engineers.
- **Compliance posture**: local-first ingestion → trufflehog scan → three-tier classification (clean/strippable/dirty) → only clean+redacted syncs to cloud. Postgres RLS for tenant isolation. DPA + open-sourced scanner config as interim trust. SOC 2 deferred.

## Phase 2: Capacity math
- **Architecture sizing**: 5K-engineer ceiling. Documented migration path to 25K. Do not over-build for 25K on day one.
- **Turns/session avg**: 8. Storage = ~21KB/session compressed. ~3.15 GB/day, ~95 GB/month, ~1.1 TB/year.
- **Vector store**: pgvector for v1. Migration to Pinecone/Turbopuffer triggered at ~10M vectors or HNSW rebuild pain.
- **Retention**: 90 days full traces hot in Postgres; scored summaries indefinitely; archive to Cloudflare R2 thereafter (S3-compatible API; zero egress fees; free-tier guardrail in `deploy.md`).
- **Models**: cheap tier (Haiku / Gemini Flash / Qwen / Sonnet-mini-tier) for `iter suggest` and scoring. Multi-provider routing planned to capture startup credits (OpenAI, Anthropic, Google).
- **Pricing posture**: charging from day 1. Bootcamp cohort (30 engineers) = free design-partner tier. All other users pay seat pricing. Seat revenue not expected to fully cover cost in v1, but offsets the burn.
- **Daemon transport**: persistent WebSocket. Server in Go for 3K–5K concurrent connections.
- **DB connection model**: PgBouncer transaction mode, ~50 server-side connections, separate pools for capture / query / scoring.

## Phase 3: Data layer
- **Server language**: Go.
- **Storage v1**: Postgres + pgvector + Redis. No separate vector DB.
- **Tenant isolation**: Postgres RLS on every tenant-scoped table.
- **Access patterns**: capture write, suggest read, scoring batch, dashboard read, git/outcome write, stack write, stack read.
- **Subagents**: rows in `sessions` table with self-referential `parent_session_id` FK.
- **Session lifecycle**: event-sourced child table (`session_events`). Sessions row is identity; events table is mutable lifecycle and outcome data.
- **Stacks table**: separate from sessions. Subject to trufflehog scan + 3-tier classification. `notes: text` replaces unbounded JSON.
- **Stack sharing model**: lightweight simulator — captures wrapped solutions (harnesses, skills, doc references) NOT raw configs or env values. Scope = team only. Granularity = whole team or individual teammate. On share, user can deselect any included file reference.

## Phase 4: Compute layer
- **v1 backend**: one Go binary. One Postgres. One Redis. Modal for GPU/LLM-heavy nightly scoring. Railway for the app + cron + webhooks.
- **Durable queue**: Redis Streams. No Kafka, no NATS, no SQS.
- **Nightly scoring**: Modal (GPU-ready for future fine-tuned scorer). Other scheduled jobs on Railway cron.
- **Failure modes**:
  - WS gateway crash: daemon buffers to local SQLite WAL, replays on reconnect.
  - Postgres down: `iter suggest` falls back to Redis-cached suggestions. Capture buffers in Redis Streams (24h TTL).
  - LLM provider down: `iter suggest` surfaces the failure upstream; never blocks waiting for retries.
  - Post-ingestion leak detected: delete-cascade session_id everywhere; write audit-log entry.

## Phase 5: API surface
- **Wire formats**: REST/JSON for CLI + dashboard + webhooks. WebSocket for daemon ↔ cloud.
- **Naming**: "stack" (not "env"). Endpoints: `/v1/stack/me`, `/v1/stack/:user`, etc.
- **Suggest request schema**: includes `effort` (low|med|high|xhigh|max) and `tools[]` (e.g. "chrome", "browser_use", "computer_use") in session_context.
- **Confidence thresholds**: 0–0.50 suppress; 0.50–0.80 advisory; 0.80–1.00 replace.
- **`include_evidence` default**: false.
- **Rate limits**: 100/min CLI, 600/min daemon, per token.
- **Webhooks at v1**: GitHub + Linear, HMAC-verified, Idempotency-Key required.
- **Auth**: WorkOS. Device-code flow for CLI/daemon. Token stored in macOS Keychain. Token carries tenant_id claim.
- **Versioning**: `/v1/` prefix; v2 dual-runs for ≥6 months.
- **Idempotency-Key header**: required on all POST endpoints.
- **No GraphQL, gRPC, SSE streaming, public API, or NL search in v1.**

## Phase 6: UI/UX
- **Mac app shell**: SwiftUI native.
- **Component library**: native SwiftUI components.
- **Server state management**: native (URLSession + actors / Observation framework).
- **Real-time**: suggestion popover synchronous; menubar polls local daemon 5s; dashboard fetches on mount + focus + manual refresh; daemon↔cloud WebSocket.
- **Daemon ↔ Mac app IPC**: Unix domain socket.
- **Screen inventory**: 9 screens + 1 transient (onboarding wizard, menubar dropdown, dashboard/me, dashboard/team, session detail, stack/me, stack/:user, settings, suggestion popover).
- **Anti-screens** (out of v1): no NL search, no public profiles, no leaderboard, no in-app editing of CLAUDE.md / AGENTS.md / skill.md, no in-app billing/admin.
- **UX answers**:
  - Popover when terminal is fullscreen → native macOS notification.
  - CLI default output → pretty text. `--json` flag for skill.md / agent invocation.
  - User leaves team → data is nuked (GDPR-style hard delete). Team aggregates exclude former members.
  - Subagents → collapsed into parent in list view; expandable.
  - "Copy to clipboard" wording, not "Replace." Suggestion never injects into terminal directly.
- **Stack profile**: pills indicating active stack, not a runtime environment switch. Simulation happens via git worktrees in chosen directories.
- **3-party handshake** for email-domain tenant suggestion: domain match suggests, admin approves, user accepts.

## Phase 7: Reliability
- **Failure inventory**: 20 items, all "mitigate" with the candidate mitigations.
- **Reliability patterns at v1**: circuit breaker, retry + exp backoff + jitter, health checks, idempotency keys, feature flags (Postgres + Redis), DLQ via Redis Streams `dlq:*`.
- **Observability stack**:
  - Logs: Sentry for app errors + BetterStack logs.
  - App agent observability: self-hosted Langfuse on Railway for LLM/agent traces.
  - Metrics + uptime + status page + on-call: BetterStack (single vendor).
  - Email notifications only (no SMS/PagerDuty for v1).
- **Minimum alerts**: `iter suggest` P99 > 1.5s for 5m, error rate >1% for 5m, scoring batch fails, Postgres connection exhaustion, WS connection count over threshold, trufflehog scan failure rate > 0.1%.
- **Secrets management at v1**: Railway built-in env vars. Doppler deferred.
- **Audit log table**: added to schema; tenant-scoped via RLS; null-tenant rows reachable only by `iter_batch` BYPASSRLS role.

## Phase 8: Scaling story
- **5 monitored migration triggers** added to BetterStack at v1 launch.
- **§8c architectural progression**: documented plan, not a v1 build target.
- **Modal warm pool**: N=2 workers always running; spin beyond on demand.
- **pgvector rebuild memory plan**: pre-budget a one-time large-RAM Postgres instance for the final HNSW rebuild before Pinecone/Turbopuffer migration.
- **WebSocket LB**: verify Railway WS support is production-ready before writing gateway code; fallback = host gateway on Fly.io.
- **25K engineer ceiling on this architecture**. Beyond that, architecture is redesigned, not extended.

## Phase 9: Build order
1. Data model
2. Tests for business logic
3. Storage layer + ops setup
4. API endpoints
5. Errors, edge cases
6. Tests (unit + integration + e2e)
7. Observability
8. UI polish

Details in `ARCHITECTURE.md` §9.
