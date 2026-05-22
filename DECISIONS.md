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

## Implementation decisions

- **Migration runner**: `goose` (v3). Picked over `golang-migrate` because (a) goose uses a single `.sql` file per version with `-- +goose Up`/`Down` markers, matching the `0001_initial.sql` filename called for in `ARCHITECTURE.md` §9; (b) Go-native and importable as a library so the cloud binary can embed migrations and run them on boot; (c) supports `StatementBegin/End` for DO-blocks needed for idempotent role creation. Tracked via the `goose_db_version` table.
- **Local dev DB**: `pgvector/pgvector:pg16` docker image on port 5433, owned by `make db-up`/`db-down`. Default `DATABASE_URL` points at it; override to target Railway dev.
- **`schema.sql` retired**: removed from repo root. `migrations/0001_initial.sql` is canonical. Maintaining a parallel snapshot would drift; the migration directory is the source of truth.
- **`iter_batch` role in initial migration**: kept inline in `0001_initial.sql` (wrapped in an idempotent DO block) to preserve semantic equivalence with the retired `schema.sql`. The application role (without `BYPASSRLS`) plus role-credential separation lands in a follow-up migration per issue 003.
- **Go module path**: `github.com/iter-dev/iter`. Placeholder org per issue 007; will be moved if/when the canonical GitHub org is registered. Go 1.22 declared as the minimum in `go.mod` (toolchain may be newer; module compatibility floor is 1.22).
- **Build runner**: `Makefile` (extended; no separate `Taskfile`). Already in use for migration helpers; adding `test`/`lint` targets there avoids splitting the developer entrypoint across two tools.
- **Lint config**: `.golangci.yml` enables `errcheck`, `govet`, `staticcheck` (linters) and `gofmt` (formatter) under the golangci-lint v2 schema. Minimum-viable per issue 007; not tuned.
- **Step 2 package layout**: `internal/{scoring,suggest,redact,signals,denylist}` — one package per pure-function domain that Step 2 covers. `pkg/contracts` and the rest of the Step 3 layout (`cmd/server`, `internal/db`, `internal/api`, ...) are intentionally deferred to Step 3.
- **`pkg/contracts` introduced early**: created in Step 2 (not Step 3) so that `internal/scoring` can type-check against the same wire shapes that `contracts.py` defines. Only the scoring-related types (`ScoreSignals`, `CompositeScoreInputs`, `CompositeScoreOutput`) are mirrored at this point; the rest land alongside the packages that need them. `contracts.py` remains canonical until the Go server is written.
- **Composite scoring formula (v1)**: weighted average of present signals, weights renormalized over the subset actually supplied. Weights: `durability_7d=0.25`, `durability_30d=0.15`, `peer_reuse_count=0.20`, `self_reuse_count=0.10`, `override_rate=0.10` (inverted: contributes `1 - override_rate`), `suggestion_acceptance=0.20`. Reuse counts are mapped to `[0,1)` via `1 - exp(-n/3)`. Floats are clamped to `[0,1]`; NaN is treated as a missing signal; negative reuse counts clamp to zero. `wall_time_ms`, `turn_count`, and `contributor_weight` are accepted on the input type for forward-compat but do not contribute to the composite at v1. Formula is a placeholder calibrated to satisfy the monotonicity / boundedness / determinism invariants from `ARCHITECTURE.md` §9 Step 2; the nightly Modal scorer is expected to replace it.
- **Score input validation policy**: invalid float inputs (NaN, out-of-range) are silently coerced rather than returning an error, because `Composite` is invoked from the nightly batch over historical rows where rejecting a single dirty row would block the whole tenant's scoring. NaN → missing; negative or `>1` floats → clamped to `[0,1]`; negative reuse counts → clamped to 0. Recorded here so the suggest-side decision function (issue 009) can pick a different stance (likely stricter) without ambiguity.
- **Suggestion decision function (`internal/suggest.SuggestionAction`)**: the locked confidence thresholds (`<0.50` Suppress, `0.50–0.80` Advisory, `≥0.80` Replace) live in exactly one place — `internal/suggest/suggest.go`. A grep-equivalent AST scan (`internal/suggest/literals_test.go`) walks `internal/` and fails the build if the float values `0.5` or `0.8` appear in any non-test `.go` file outside `internal/suggest/`, enforcing the "never reimplement" invariant from `CLAUDE.md`. The `Action` enum (`Suppress | Advisory | Replace`) is mirrored in `pkg/contracts/suggest.go` so the daemon, CLI, and dashboard share one set of string constants.
- **Suggest-side input policy (issue 009)**: the request path is latency-sensitive (≤1s P99) and synchronous with the user typing, so `SuggestionAction` never returns an error. Out-of-band inputs degrade to the safest action: `NaN` → `Suppress` (refuse to surface a possibly-bad suggestion), `confidence < 0` → `Suppress` (treated as 0; same reason), `confidence > 1` (including `+Inf`) → `Replace` (clamped to 1; the upstream scorer already clamps to `[0,1]`, so this is defense-in-depth for any future signal source that doesn't). This is intentionally less strict than what the scoring layer accepts: the scorer cleans up its own inputs, so by the time `SuggestionAction` sees a value it should already be in `[0,1]`; the policy above is a contract for what happens if that invariant is violated.
- **Signal aggregation `SessionEvent` shape** (issue 011): `pkg/contracts.SessionEvent` is the in-process Go projection of the wire-level `TraceEvent` from `contracts.py` plus the `session_events` table from `migrations/0001_initial.sql`. Fields: `ID string` (dedup key, opaque to the aggregator — daemon msg_id or DB row id), `SessionID string`, `ParentSessionID *string` (non-empty ⇒ subagent), `Type contracts.EventType` (mirroring the Python enum), `OccurredAt time.Time`, `Payload map[string]any`. `ParentSessionID` carries the subagent partition rather than a separate `is_subagent` bool because the wire envelope already transports the parent uuid and a redundant bool would risk drift. The closed `EventType` enum is mirrored verbatim from `contracts.py` (sixteen kinds) and matches the `session_events.event_type` CHECK constraint. `contracts.py` remains canonical until the Go server is written.
- **Signals derived in v1** (issue 011): `internal/signals.Aggregate` populates `peer_reuse_count`, `self_reuse_count`, `override_rate` (overrides / turn_completed, clamped to `[0,1]`; requires `overrides>0 AND turns>0` — zero overrides is "no signal", not a real 0.0), and `suggestion_acceptance` (accepted / (accepted + rejected); denominator>0 rule — 0 acceptances among rejections IS a real 0.0). `durability_7d` and `durability_30d` are NOT populated by per-session aggregation — they require windowed analysis across sessions and remain the nightly Modal batch's responsibility. Counters of zero surface as `nil` pointers; a present-but-zero pointer would be indistinguishable from a real observation in downstream aggregations.
- **Subagent independence in aggregation** (issue 011): `internal/signals` exposes two entry points, `Aggregate` and `AggregateSubagent`, both consuming the same `[]SessionEvent` slice. Each ignores the other partition (determined by `SessionEvent.IsSubagent()`), preserving the "subagent independent scoring" invariant from `ARCHITECTURE.md` §9 Step 5. Callers can pass an unsorted mixed slice and call both functions to obtain the two independent score-signal vectors.
- **Dedup key for replay idempotency** (issue 011): `SessionEvent.ID` is the equality key. Events with empty IDs are NOT deduplicated — the daemon WAL can produce wire messages before they receive a server-assigned id, and at that stage replays are still controlled by the WS gateway's ack protocol. Two events with the same ID accumulate once; the order in the input slice does not matter (first-seen wins, but counters are the same regardless because dedup happens before the switch).
- **Dangerous-pattern deny-list matching policy** (issue 012): the deny-list is declared as data — a slice of `{id, regex, description}` literals in `internal/denylist/patterns.go`. Regexes compile once at package init via `regexp.MustCompile`. Match scope is **shell-command boundary**, not substring: a pattern hits only when it appears at the start of input, immediately after a newline, `;`, `|`, `&`, backtick, or `$(`, optionally preceded by `sudo`/`doas`/`env`/`exec`/`nohup`/`time`. This means prose like `"the rm -rf flag is dangerous"` is **not** flagged, while `"ls; rm -rf /"` and `"echo x | rm -rf /tmp"` are. Two exceptions are intentionally **not** boundary-gated: (a) destructive SQL (`DROP TABLE`/`DROP DATABASE`/`TRUNCATE TABLE`), so injection payloads like `'; DROP TABLE users; --` still match, and (b) the fork-bomb literal `:(){:|:&};:` because the byte sequence is unambiguous in any context. Shell line-continuations (`\` + newline) inside a command are pre-normalized to a single space in `Contains`, so a `rm \\\n-rf foo` bypass collapses to the canonical form before regex evaluation. Unicode lookalikes are **not** normalized: a Cyrillic `е` in place of ASCII `e` does not match, on the rationale that the shell would not execute it as `rm` either. The returned `patternID` is opaque and intended only for security event logs; the public suggestion-output path returns only the boolean and emits a generic "suppressed" message (per `ARCHITECTURE.md` §9 Step 5 "deny-list hit → silent"). Perf budget: <5ms over 10KB input (well below the 1s suggest-path latency invariant).
- **Trufflehog version pin (issue 010)**: `3.95.3`. Recorded in `trufflehog.version` at the repo root and mirrored as `TRUFFLEHOG_VERSION` in the `Makefile`. `internal/redact` reads the pin file at test time and asserts the installed binary matches; CI is expected to install this exact version. Upgrades are deliberate: bumping the pin requires re-running the corpus tests in `internal/redact/testdata/` to catch detector regressions.
- **PII redaction policy (issue 010)**: `internal/redact.Classify` applies a small in-tree PII detector in addition to trufflehog's secret detectors. Emails (matched by `\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b`) and US-style phone numbers are `strippable`, replaced in place with `[REDACTED_EMAIL]` / `[REDACTED_PHONE]`. Physical street addresses (heuristic: digit run + word + street-suffix) are classified `dirty` — the heuristic is too noisy to redact cleanly and full address corpora are not appropriate for cloud sync. Personal names are NOT detected: a regex over free text against code yields unacceptable false positives, and the trufflehog secret path is the higher-value control. Recorded here as the binding v1 policy; tightening (e.g. ML-based PII) is a Step 5 hardening item, not Step 2.
- **Trufflehog wrapper failure mode (issue 010)**: per `ARCHITECTURE.md` §9 Step 5 "trufflehog failure (fail-closed)", any internal failure of the wrapper — missing binary, non-zero exit, malformed JSON, timeout — returns `dirty` with the original payload bytes (never partial). The `Classification` zero value is `dirty` by design so a forgotten code path defaults to fail-closed. Errors are surfaced alongside the classification so the caller can log them; the classification itself is authoritative.
- **HTTP router (issue 028)**: `github.com/go-chi/chi/v5`. Picked over `gin` because (a) chi handlers are plain `http.HandlerFunc`, keeping middleware composable with any stdlib-shaped library; (b) the middleware sketches in issue 029 are already written against `func(http.Handler) http.Handler`, which chi accepts natively while gin would need wrapping; (c) WebSocket upgrade in issue 043 wants direct access to `http.ResponseWriter` + `*http.Request`, awkward through `gin.Context`; (d) smaller dep graph, no global state, no transitive web framework. Trade-off accepted: gin has more batteries (binding, validation, recovery built-in); we re-implement those minimally in `internal/api/middleware/` to keep lock-in low. Do not import `chi/middleware` — middleware concerns live in `internal/api/middleware/` so we can rip chi out later if we ever need to without rewriting handlers.
- **Server skeleton scope (issue 048)**: this slice ships only the §9 Step 3 package layout (`cmd/server`, `internal/{api,ws,ingest,auth,app}`) and a boot entry that binds `PORT` and drains on SIGTERM/SIGINT with a 10s budget. No chi, no pgx, no redis — those land in 028/049/050 respectively, keeping `go.mod` clean through 048. The placeholder `/` handler returns 503 with a "skeleton — handlers land in issue 028" body so any traffic reaching this binary today is loud, not silent. `/health` deliberately punted to 028 alongside the chi wiring (the issue brief's "health endpoint" line is folded into 028).
- **`Deps` location (issue 048)**: `internal/app/deps.go`, not `cmd/server/deps.go`. Rationale: api/ws handler tests will need to construct a `Deps` (with a test logger, a fake pool, etc.) and `cmd/...` packages are not importable. Putting the bag under `internal/app` lets both `cmd/server` and `internal/api` import the same type. Deps starts with `Logger *slog.Logger` and `BuildVersion string`; comment block enumerates the deferred fields (`DB`, `Redis`, `Auth`, `Modal`) so subsequent issues have a clear extension point.
- **pgx exec mode for PgBouncer transaction-mode (issue 049)**: `QueryExecModeCacheDescribe`, forced at the connection layer in `internal/db.NewPool`. Picked over `QueryExecModeExec` (no caching at all) because cache_describe still caches column-type descriptions on the pgx side (client-side, per `pgx.Conn`) so result decoding stays typed and fast; picked over `QueryExecModeCacheStatement` (the pgx default) because cache_statement creates server-side named prepared statements that PgBouncer's transaction-mode pooling invalidates between client checkouts. cache_simple_protocol was rejected because it loses parameter typing. Documented in `internal/db/doc.go`; the integration test does not exercise the PgBouncer failure mode (bare Postgres tolerates either) so this stays a code-review invariant.
- **Tenant helper as the single sanctioned entrypoint (issue 049)**: `internal/db.WithTenant(ctx, pool, tenantID, fn)` is the only function in the request path that opens a Postgres transaction. It validates `tenantID` as a UUID (then injects via `SET LOCAL` because that command does not bind parameters), stashes the resulting `pgx.Tx` in `ctx` so repository functions can pull it via `db.FromContext`, and rolls back on any error. The matching BYPASSRLS path is `WithBatch`, which requires a separate `*pgxpool.Pool` built from `$DATABASE_URL_BATCH`; the deps `BatchDB` field is reserved but unset in v1 because the Modal worker (issue 046) opens its own connection. Calling `pool.Query` / `pool.Exec` directly from request handlers is a code-review rejection — the helpers exist precisely so RLS can never be skipped by accident.
