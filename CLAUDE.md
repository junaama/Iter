# CLAUDE.md

Guidance for Claude Code when working in this repository.

After any change, make a conventional commit scoped to the work you did with the files you touched.

## State

Iter v1 is implementation-stage, not design-stage. The repo has a Go module/server, Postgres migrations, Redis-backed workers, REST/WebSocket/webhook handlers, ingestion/redaction/embedding/scoring/archive code, and a SwiftUI macOS app plus daemon under `mac/`.

Docs are binding. When behavior changes, update the matching spec, decision, contract, deployment, or design artifact in the same commit.

Key locations:

- `ARCHITECTURE.md`, `DECISIONS.md`, `DESIGN.md`, `deploy.md` - product, architecture, design, and ops truth.
- `contracts.py`, `pkg/contracts/` - daemon, CLI, server, dashboard, and webhook wire contracts.
- `cmd/`, `internal/`, `pkg/` - Go binaries, services, workers, and packages.
- `migrations/`, `scripts/verify-migration.sh` - goose schema, RLS, pgvector, and verifier.
- `mac/IterApp/` - SwiftUI app, daemon client, and design system.
- `issues/` - claim-based work queue.

## Product

Iter is a Mac app for teams using coding agents such as Claude Code, Codex, Pi, OpenCode, and Gemini CLI. A local daemon ingests traces, redacts secrets before cloud sync, scores outcomes, and surfaces prompt refinements through `iter suggest`.

## Common checks

- Go: targeted package tests, or `go test ./...`.
- Repo checks: `make test`, `make lint`, `make test-rls`, `make test-redis`.
- Migrations: `make migrate-up`, `make db-verify`, and `scripts/verify-migration.sh`.
- SwiftUI: `swiftlint lint mac/IterApp` and `xcodebuild -project mac/IterApp.xcodeproj -scheme IterApp -configuration Debug -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build`.
- Mac dev launch: `HEADLESS=1 make mac-dev` for CI-style verification.

## Shared issue workflow

- Claim one AFK issue before exploring: move it from `issues/` to `issues/in-progress/`.
- Do not pick HITL, deferred, blocked, or already claimed issues.
- Commit with a conventional commit message.
- Release the issue before exit: move complete work to `issues/done/`, or return it to `issues/` with a blocker note.
- In a dirty shared tree, preserve unrelated edits and stage only task-local files or hunks.

## Invariants

- Suggestion thresholds live in the pure decision function: Python `suggestion_action(confidence, refined_prompt)` and Go `suggest.SuggestionAction`. Do not reimplement the threshold literals elsewhere.
- Redaction is `clean | strippable | dirty`; only `clean` and redacted `strippable` records reach the cloud.
- Tenant-scoped tables require `tenant_id`, cascade delete to `tenants(id)`, RLS, and verifier coverage.
- `iter suggest` has a 1s P99 end-to-end latency budget.
- Retention is 90 days hot in Postgres, then Cloudflare R2 via `archive_pointers.object_uri`; scored summaries stay indefinitely.
- Stacks can capture harnesses, skills, doc references, and notes, never raw configs, env values, secrets, or MCP credentials.
- Suggestions never inject into a terminal directly; UI wording is "Copy to clipboard."
- Wire formats are REST/JSON for CLI, dashboard, and webhooks; WebSocket for daemon to cloud. No GraphQL, gRPC, SSE, public API, or natural-language search across sessions in v1.
- `Idempotency-Key` is required on POST endpoints; webhooks require HMAC verification.
- Dangerous suggestions such as `rm -rf`, `DROP TABLE`, and `git push --force` are blocked silently and logged as security events.
- `contracts.py` stays the canonical compatibility contract; mirror shared wire changes in `pkg/contracts/`.
- Shipped migrations are immutable; add schema changes in a new goose migration.
- Preserve the cascade-delete chain from sessions to events, embeddings, scores, outcomes, and any new session-linked data.

## Where to look first

- V1 scope: `ARCHITECTURE.md` section 1.
- Decisions: `DECISIONS.md`.
- API contracts: `contracts.py`, then `pkg/contracts/`.
- Failure modes: `ARCHITECTURE.md` section 7.
- Build order: `ARCHITECTURE.md` section 9.
- Visual language: `DESIGN.md`, then `mac/IterApp/DesignSystem/`.
