# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

After any change, make a conventional commit scoped to the work you did with the files you touched.

## Repository state

This is a **design-stage** repository. No application code, build system, tests, or CI exist yet. The artifacts in this directory are the binding source of truth for what Iter v1 will be:

| File | Role |
|---|---|
| `ARCHITECTURE.md` | The spec. Source of truth for v1 scope. When implementation drifts, update the doc. |
| `DECISIONS.md` | Decision log. Every line is binding; change the doc and the artifact together. |
| `contracts.py` | Wire types (pydantic). Daemon/CLI/dashboard/webhook boundary. The only place untyped JSON is allowed. |
| `schema.sql` | Postgres 16 schema with `pgvector`, `pgcrypto`, `citext`. RLS on every tenant-scoped table. |
| `deploy.md` | Hosting + env vars + rollback. Railway-centric. |
| `testing.md` | Test plan (Go-based, not yet implemented). |
| `DESIGN.md` | Visual language + locked design tokens for the SwiftUI app. Points at the prototype in `design/dashboard-prototype/`. |

Build commands referenced in `testing.md` and `deploy.md` (`make test`, `make mac-release`, `railway up`) **do not work yet** — there is no `Makefile`, no `go.mod`, no Mac app project. Do not run them. If asked to implement, the build order is documented in `ARCHITECTURE.md` §9.

## Product in one paragraph

Iter is a Mac app for teams using coding agents (Claude Code, Codex, Pi, OpenCode, Gemini CLI). A local daemon ingests agent traces from on-disk session files, redacts secrets via trufflehog before any cloud sync, scores outcomes, and surfaces prompt refinements via `iter suggest` at task-start. Distributed via iter.dev. Primary buyer is the team; solo devs get a local-only free tier.

## Target architecture (not yet built)

- **One Go binary** on Railway runs: WebSocket gateway (daemon ↔ cloud), `iter suggest` REST API, webhook receivers (GitHub + Linear), ingestion processor, embedding worker, dashboard API, archive cron.
- **Postgres 16 + pgvector** (single store at v1) — HNSW index on `session_embeddings`. RLS enforced via per-transaction `SET LOCAL app.current_tenant = '<uuid>'`. Privileged `iter_batch` role has `BYPASSRLS` for nightly scoring + archive only; never reachable from request path.
- **Redis** as cache and Redis Streams durable queue (no Kafka/NATS/SQS).
- **Modal** for nightly scoring batch (warm pool N=2, GPU-ready).
- **WorkOS** for auth (device-code flow for CLI/daemon; tokens carry `tenant_id` claim, stored in macOS Keychain).
- **SwiftUI** native Mac app + daemon. IPC over Unix domain socket. Daemon writes traces to local SQLite WAL before sending; replays on WS reconnect.

Architecture is sized for ~5K engineers. Migration triggers to ~25K are documented in `ARCHITECTURE.md` §8 — they are monitoring thresholds, **not** v1 build targets. Do not pre-build for 25K.

## Locked invariants

These are decided. Don't propose alternatives without explicit direction:

- **Confidence thresholds** (`contracts.py:86-87`): `<0.50` suppress, `0.50–0.80` advisory, `≥0.80` replace. Clients call the pure `suggestion_action(confidence, refined_prompt)` decision function — never reimplement the thresholds elsewhere.
- **Three-tier redaction classification**: `clean | strippable | dirty`. Only `clean` and successfully-redacted `strippable` records reach the cloud. `dirty` stays on-device.
- **Tenant isolation**: RLS on every tenant-scoped table. New tables MUST add a `tenant_id` column, an RLS policy, and the cascade-delete FK to `tenants(id)`.
- **`iter suggest` latency budget**: ≤1s P99 end-to-end. Budget breakdown in `ARCHITECTURE.md` §2.
- **Retention**: 90 days hot in Postgres, then Cloudflare R2 via `archive_pointers` (column: `object_uri`). Scored summaries kept indefinitely. R2 free-tier guardrail + 80% alerts documented in `deploy.md` "R2 usage monitoring."
- **Stacks** capture wrapped solutions (harnesses, skills, doc references, notes) — **never** raw configs, env values, secrets, or MCP credentials.
- **Suggestions never inject into a terminal directly** — clipboard only. UI wording is "Copy to clipboard," not "Replace."
- **Wire formats**: REST/JSON for CLI/dashboard/webhooks, WebSocket for daemon↔cloud. **No** GraphQL, gRPC, SSE, public API, or NL search across sessions at v1.
- **Idempotency-Key** required on all POST endpoints; **HMAC verification** required on webhooks.
- **Dangerous-pattern deny-list** (e.g. `rm -rf`, `DROP TABLE`, `git push --force`): blocked silently in suggestion output; logged as security event.

## Working with `contracts.py`

This file is the boundary. Any change to a wire type is a breaking change to one or more of: the daemon, the CLI, the dashboard, the Go server, or external webhook senders. When editing:

- Keep it pure: no I/O, no business logic, no impure handlers.
- All models use `ConfigDict(extra="forbid")` except where `extra="allow"` is intentional (e.g. `ScoreSignals` — signals evolve) or `extra="ignore"` (inbound webhooks — third-party payloads).
- Discriminated unions use `Field(discriminator="type")`. Add new WS message types to both the type itself **and** the `ClientMessage`/`ServerMessage` `Union`.
- The Python file exists because contracts predate the Go implementation. When the Go server is written, mirror these types in `pkg/contracts` (Go). Until then, `contracts.py` is canonical.

## Working with `schema.sql`

- This file is the initial migration. Once the repo gains a `migrations/` directory (planned: `0001_initial.sql`), schema changes go there — never edit a shipped migration.
- New tenant-scoped tables MUST: include `tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`, `ALTER TABLE ... ENABLE ROW LEVEL SECURITY`, and a `tenant_isolation` policy using `current_setting('app.current_tenant')::uuid`.
- HNSW indexes (`session_embeddings`, `suggestions.source_embedding`) use `m=16, ef_construction=64`. Rebuild plan when row count >10M is documented in §8.
- Cascade-delete chain matters for the post-ingestion-leak failure mode: deleting a `session_id` must cascade to events, embeddings, scores, and outcomes. Verify when adding new session-linked tables.

## Where to look first

- "What does v1 include?" → `ARCHITECTURE.md` §1.
- "Why was X decided that way?" → `DECISIONS.md`, organized by phase.
- "What's the build sequence?" → `ARCHITECTURE.md` §9.
- "How does failure mode X behave?" → `ARCHITECTURE.md` §7 table.
- "What's the API contract for X?" → `contracts.py` first, `ARCHITECTURE.md` §5 second.
- "What color / font / row height / radius should this use?" → `DESIGN.md`. Don't introduce new tokens without updating it first.
- "What does the Me/Team/Session screen look like?" → `design/dashboard-prototype/project/`. Reference only — pixel-match in SwiftUI, don't port HTML/CSS structure.
