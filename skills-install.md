# Iter — Skills & tooling install

Skills and CLI tools required to implement Iter v1 per `ARCHITECTURE.md` §9 build order.

**Already installed**: `gh`, `railway`.

## Install priority

When a tool has both a CLI binary and a skills.sh skill, install the **CLI first** — it's the executable that actually does work. Add the skill on top if it provides procedural knowledge the agent should load.

## How to install a skills.sh skill

```bash
npx skills add <github-repo>            # whole repo
npx skills add <github-repo> --skill X  # single skill from a repo
```

skills.sh skills are loaded by Claude Code, Cursor, Codex, and other agents. See [skills.sh/docs/cli](https://www.skills.sh/docs/cli).

## Manual auth required after install

Many of the CLIs below need an interactive login I cannot run for you. After install, run these yourself:

| CLI | Auth command | Notes |
|---|---|---|
| `gh` | `gh auth login` | (already installed; may already be authed) |
| `railway` | `railway login` | (already installed; may already be authed) |
| `modal` | `modal token new` | Opens browser; pastes token back. |
| `sentry-cli` | `sentry-cli login` | Browser-based; choose the Iter org. |
| `wrangler` | `wrangler login` | Cloudflare CLI; manages the `iter-archive-prod` R2 bucket and Analytics token from `deploy.md`. Browser-based login. |
| `xcrun notarytool` | `xcrun notarytool store-credentials` | Apple ID + app-specific password; one-time. |
| `trufflehog` | none | No auth needed. |
| `workos` (no CLI) | env vars only | Set `WORKOS_CLIENT_ID`, `WORKOS_API_KEY`, `WORKOS_REDIRECT_URI` per `deploy.md`. |
| LLM/embedding providers | env vars only | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_AI_API_KEY`, `TOGETHER_API_KEY`, `VOYAGE_API_KEY`. |

## By build phase

Order matches `ARCHITECTURE.md` §9.

### Step 1 — Data model (Postgres + pgvector)

CLI:
```bash
brew install libpq && brew link --force libpq    # psql
brew install pgcli                                # nicer REPL for tenant-context queries
brew install goose                                # forward-only migrations (deploy.md runs on boot)
```

Skill:
```bash
npx skills add timescale/pg-aiguide --skill pgvector-semantic-search
```
Source: <https://www.skills.sh/timescale/pg-aiguide/pgvector-semantic-search>. Covers vector storage, HNSW indexing, query optimization — directly applicable to `session_embeddings` + `suggestions` HNSW indexes in `schema.sql`.

Also already loaded in this environment: `supabase-postgres-best-practices` (RLS patterns).

### Step 2 — Business-logic tests

CLI:
```bash
brew install go golangci-lint gotestsum
brew install uv                                   # for contracts.py
uv pip install pydantic ruff black mypy
```

Skills:
```bash
npx skills add jeffallan/claude-skills --skill golang-pro
npx skills add trailofbits/skills --skill modern-python
```
Sources: <https://skills.sh/jeffallan/claude-skills/golang-pro>, <https://skills.sh/trailofbits/skills/modern-python>. `golang-pro` enforces `gofmt`, `golangci-lint`, table-driven tests with ≥80% coverage, and fuzzing — matches the bar in `testing.md`.

### Step 3 — Storage layer + ops setup

CLI:
```bash
brew install --cask docker        # or: brew install colima docker — testcontainers backing
brew install redis                # redis-cli for Streams + DLQ inspection
brew install trufflehog           # pre-ingestion secret scanning (DECISIONS.md phase 1)
brew install cloudflare-wrangler  # Cloudflare R2 archive bucket, version-restore on rollback, free-tier monitoring
uv pip install modal-client       # Modal CLI for nightly scoring batch
```

Manual auth after install: `modal token new`, `wrangler login` (browser flow; select the Iter Cloudflare account), `railway login` (if not already).

`wrangler` covers everything we need from R2 at v1: bucket create/list/delete, object get/put/list, lifecycle rules, and analytics queries. The Go binary itself talks to R2 via the AWS SDK with a custom endpoint (`$R2_ENDPOINT`) — `wrangler` is for human/CLI use only. If you prefer the AWS CLI for one-off scripts, `brew install awscli` and point `--endpoint-url` at `$R2_ENDPOINT`; not required for v1.

Skills:
```bash
npx skills add workos/skills                                              # both workos + workos-widgets
npx skills add davila7/claude-code-templates --skill modal
npx skills add redis/agent-skills --skill redis-development
npx skills add davila7/claude-code-templates --skill railway-deployment
npx skills add railwayapp/railway-skills --skill use-railway
```
Sources: <https://github.com/workos/skills>, <https://www.skills.sh/davila7/claude-code-templates/modal>, <https://skills.sh/redis/agent-skills/redis-development>, <https://skills.sh/davila7/claude-code-templates/railway-deployment>, <https://skills.sh/railwayapp/railway-skills/new> (the actual skill in the repo is named `use-railway`).

The `workos/skills` repo is a router skill plus 40+ references covering AuthKit setup, SSO, Directory Sync, RBAC, and backend SDK guidance — fits the device-code OIDC flow + SAML in `ARCHITECTURE.md` §5. The `redis-development` skill has a "Streams vs Pub/Sub" rule that matches v1's durable-queue choice in `DECISIONS.md` phase 4.

TruffleHog has no skills.sh skill; the CLI binary is sufficient. The Go server vendors detectors; the CLI is needed only to generate `test/fixtures/secrets/` corpus.

### Step 4 — API + WebSocket gateway

CLI (local dev tooling):
```bash
brew install hey websocat httpie
```
- `hey` / `vegeta` — smoke load test `/v1/suggest`
- `websocat` — manually poke the WS gateway
- `httpie` — HMAC-signed webhook debugging

No dedicated WebSocket-gateway skill on skills.sh. Use `find-docs` / `ctx7` (already loaded) for `nhooyr.io/websocket`, `chi`, `pgxpool` API details.

### Step 5 — Mac app + daemon (SwiftUI)

CLI:
```bash
# Xcode (full, not just CLT) — install from App Store
brew install xcbeautify swiftlint create-dmg
```

Manual auth after install: `xcrun notarytool store-credentials` (Apple ID + app-specific password). Required for the `make mac-release` notarization step in `deploy.md`.

Skill:
```bash
npx skills add avdlee/swiftui-agent-skill --skill swiftui-expert-skill
```
Source: <https://skills.sh/avdlee/swiftui-agent-skill/swiftui-expert-skill>. Covers iOS 26+ and macOS, state management, view composition, performance — applicable to the 9 screens in `ARCHITECTURE.md` §6.

### Step 6 — Observability

CLI:
```bash
brew install getsentry/tools/sentry-cli
```

Manual auth after install: `sentry-cli login` (browser flow; choose Iter org).

BetterStack and Langfuse have no CLIs — both are dashboard-configured. Token via `BETTERSTACK_SOURCE_TOKEN` and `LANGFUSE_*` env vars per `deploy.md`.

Skills:
```bash
npx skills add membranedev/application-skills --skill better-stack
npx skills add langfuse/skills --skill langfuse
# sentry/dev is a private GitHub repo (auth fails) — sentry-cli binary above is sufficient
```
Sources: <https://www.skills.sh/membranedev/application-skills/better-stack>, <https://skills.sh/langfuse/skills/langfuse-observability> (the skill in the repo is named `langfuse`, not `langfuse-observability`). Together these cover the BetterStack + Langfuse parts of the v1 observability stack per `DECISIONS.md` phase 7.

The `langfuse` skill is also already loaded as a built-in in this environment.

### Step 7 — LLM + embedding providers

No CLIs to install — all access via Go SDK behind the LLM/embedding provider abstractions (`ARCHITECTURE.md` §3 build step). Env vars only: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_AI_API_KEY`, `TOGETHER_API_KEY`, `VOYAGE_API_KEY`.

Skill:
```bash
npx skills add anthropics/skills
```
Source: <https://www.skills.sh/anthropics>. Includes Anthropic SDK patterns. The `claude-api` skill is already loaded in this environment as well.

For generating realistic session fixtures in `test/fixtures/sessions/`, install the captured harness CLIs per each vendor's docs (`claude`, `codex`, `gemini`, `opencode`, `pi`) — these match `contracts.py:Harness`.

### Cross-cutting — GitHub

CLI: `gh` is already installed.

Skill:
```bash
npx skills add github/awesome-copilot   # whole repo — no isolated gh-cli skill exists
```
Source: <https://skills.sh/github/awesome-copilot/gh-cli>. The repo doesn't expose `gh-cli` as a standalone skill; installing the repo pulls in related skills like `github-issues`, `github-release`, and the `create-github-issue-*` family — useful for the webhook receiver wiring (`/v1/webhooks/github`) and Iter dogfooding against its own repo per `deploy.md` first-deploy checklist.

## One-shot install

```bash
# CLI binaries
brew install libpq pgcli goose go golangci-lint gotestsum \
             redis trufflehog cloudflare-wrangler2 hey websocat httpie \
             xcbeautify swiftlint create-dmg \
             getsentry/tools/sentry-cli \
             jq direnv
brew link --force libpq
brew install --cask docker   # or: brew install colima docker

# Python / Modal
brew install uv
uv pip install pydantic ruff black mypy modal-client

# skills.sh skills
npx skills add timescale/pg-aiguide --skill pgvector-semantic-search
npx skills add jeffallan/claude-skills --skill golang-pro
npx skills add trailofbits/skills --skill modern-python
npx skills add workos/skills
npx skills add davila7/claude-code-templates --skill modal
npx skills add redis/agent-skills --skill redis-development
npx skills add davila7/claude-code-templates --skill railway-deployment
npx skills add railwayapp/railway-skills --skill use-railway
npx skills add avdlee/swiftui-agent-skill --skill swiftui-expert-skill
npx skills add membranedev/application-skills --skill better-stack
npx skills add langfuse/skills --skill langfuse
# sentry/dev is private on GitHub — skipped; sentry-cli binary covers it
npx skills add github/awesome-copilot
npx skills add anthropics/skills
```

Then run the auth commands in the table at the top of this file.

## Skills already loaded in this environment

These appear in the session's system reminder — no `npx skills add` needed:

| Skill | Use for |
|---|---|
| `find-docs` (`ctx7`) | Up-to-date docs for any library. Use before guessing API shapes. |
| `langfuse` | Query / configure the self-hosted Langfuse on Railway. |
| `supabase-postgres-best-practices` | Postgres + RLS patterns; direct fit for v1 tenant isolation. |
| `claude-api` | Wiring the Anthropic SDK into the LLM provider abstraction. |
| `render-skill` | Alt deploy target if Railway WS production-readiness fails. |

## Not needed at v1

Explicitly out per `DECISIONS.md` — do not install:

- Doppler — Railway env vars suffice until phase 7 revisit.
- Kafka / NATS / SQS clients — Redis Streams is the durable queue.
- Pinecone / Turbopuffer SDKs — migration trigger at >10M `session_embeddings` rows, post-v1.
- Datadog / Grafana / PagerDuty — BetterStack is the single observability vendor.
- gRPC / protobuf tooling — REST + WebSocket only at v1.
