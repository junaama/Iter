# Iter

Local-dev instructions for running the three processes that make up Iter end-to-end:

1. **Server** (`cmd/server`) — Go HTTP API + WebSocket gateway + ingestion/embedding workers
2. **Daemon** (`cmd/iter-daemon`) — per-user Mac daemon that exposes a Unix socket to the app
3. **Mac app** (`mac/IterApp`) — SwiftUI client that talks to both

For product/architecture context, start with `ARCHITECTURE.md`, `DECISIONS.md`, and `CLAUDE.md`.

---

## Prerequisites

- Go 1.22+
- Xcode 15+ (`xcodebuild` on PATH)
- Docker (for local Postgres + integration tests)
- `goose` migration runner: `go install github.com/pressly/goose/v3/cmd/goose@latest`
- (Optional) Redis 7 — only needed if you want ingestion, embeddings, or the suggest cache

---

## 1. Database

Start a local pgvector Postgres on `:5433` and apply migrations:

```sh
make db-up
make migrate-up
make db-verify   # optional: asserts schema invariants
```

This creates a database at `postgres://iter:iter@localhost:5433/iter?sslmode=disable` — used as `DATABASE_URL` below.

To reset:

```sh
make migrate-reset
make db-down
```

---

## 2. Environment

The server reads config from environment variables. The repo already has a `.env` at the root with provider keys; the table below is what the server actually consults.

| Variable | Required | Notes |
|---|---|---|
| `DATABASE_URL` | yes | Local default: `postgres://iter:iter@localhost:5433/iter?sslmode=disable` |
| `DATABASE_URL_BATCH` | no | `iter_batch` BYPASSRLS role; archive cron is skipped without it |
| `REDIS_URL` | no | e.g. `redis://localhost:6379` — without it, ingest + embed workers don't start |
| `PORT` | no | Defaults to `8080` |
| `WORKOS_JWKS_URL`, `WORKOS_ISSUER` | for auth | Without both, every authenticated request returns 503. `WORKOS_JWKS_URL` = `https://api.workos.com/sso/jwks/<WORKOS_CLIENT_ID>`; `WORKOS_ISSUER` = your AuthKit domain (Dashboard → Authentication → AuthKit). |
| `WORKOS_AUDIENCE` | optional | AuthKit session JWTs don't carry `aud`; leave unset. |
| `WORKOS_API_KEY`, `WORKOS_CLIENT_ID`, `WORKOS_REDIRECT_URI` | for `/auth/*` | AuthKit login routes (device-code flow works without these) |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GOOGLE_AI_API_KEY` / `TOGETHER_API_KEY` | for LLM | Any subset; router skips missing providers |
| `VOYAGE_API_KEY` | for embeddings | Voyage is the only real provider today |
| `GITHUB_WEBHOOK_SECRET`, `LINEAR_WEBHOOK_SECRET` | for webhooks | Empty → handler returns 401 |
| `R2_ENDPOINT`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_REGION`, `R2_ARCHIVE_BUCKET`, `R2_ACCOUNT_ID` | for archive cron | All-or-nothing; cron skipped if incomplete |

Load `.env` into your shell however you prefer (e.g. `set -a; source .env; set +a`).

---

## 3. Server

```sh
export DATABASE_URL='postgres://iter:iter@localhost:5433/iter?sslmode=disable'
# optional:
# export REDIS_URL='redis://localhost:6379'
# set -a; source .env; set +a

make run
# or: go run ./cmd/server
```

Listens on `:8080`. Boot logs will tell you exactly which subsystems started and which are skipped (Redis, archive cron, AuthKit, etc.).

Smoke test:

```sh
curl -s http://127.0.0.1:8080/health
```

---

## 4. Daemon

The daemon binds a Unix socket at `~/Library/Application Support/Iter/daemon.sock` for the Mac app to connect to.

### Quick (foreground, for dev)

```sh
go run ./cmd/iter-daemon
```

Leave it running in its own terminal.

### LaunchAgent (production-style)

```sh
# Build + install
go build -o /usr/local/bin/iter-daemon ./cmd/iter-daemon
scripts/install-iter-daemon.sh

# Verify
launchctl print "gui/${UID}/dev.iter.IterDaemon"
test -S "${HOME}/Library/Application Support/Iter/daemon.sock" && echo "socket up"

# Uninstall
scripts/uninstall-iter-daemon.sh
```

Logs land in `~/Library/Logs/Iter/iter-daemon{,.err}.log`.

---

## 5. Mac app

```sh
make mac-dev
```

This builds the unsigned debug app under `mac/build/Build/Products/Debug/IterApp.app` and launches it. Set `HEADLESS=1` to build without launching.

By default the app points at `http://127.0.0.1:8080`. Override with either:

```sh
# env var (takes precedence)
export ITER_API_BASE_URL='http://127.0.0.1:8080'
make mac-dev

# or persistent UserDefaults
defaults write dev.iter.IterApp iter.api.baseURL 'http://127.0.0.1:8080'
```

If you have a bearer token instead of running the full WorkOS flow:

```sh
export ITER_API_TOKEN='…'
```

---

## End-to-end loop

In four shells:

```sh
# 1. DB
make db-up && make migrate-up

# 2. Server
set -a; source .env; set +a
export DATABASE_URL='postgres://iter:iter@localhost:5433/iter?sslmode=disable'
make run

# 3. Daemon
go run ./cmd/iter-daemon

# 4. Mac app
make mac-dev
```

Sign in from the Mac app — it uses WorkOS device-code auth against the local server.

---

## Tests

```sh
make test         # unit
make test-rls     # Postgres RLS + repos (testcontainers, ~40s)
make test-redis   # Redis streams + DLQ (testcontainers)
make lint
```

SwiftUI:

```sh
swiftlint lint mac/IterApp
xcodebuild -project mac/IterApp.xcodeproj -scheme IterApp -configuration Debug \
  -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
```

---

## Troubleshooting

- **Server logs "DATABASE_URL is required"** — env not exported into the shell running `make run`.
- **Auth middleware returns 503 `auth_unavailable`** — set the three `WORKOS_JWKS_URL` / `WORKOS_ISSUER` / `WORKOS_AUDIENCE` vars, or hit unauthenticated routes only.
- **Mac app can't reach API** — confirm `curl http://127.0.0.1:8080/health` works, then check `ITER_API_BASE_URL` / the `iter.api.baseURL` default.
- **Daemon socket missing** — make sure `~/Library/Application Support/Iter/` exists and the daemon process is alive; `lsof -U | grep daemon.sock` should show it.
- **Ingest/embed workers didn't start** — `REDIS_URL` is unset or unreachable. Start Redis (`docker run -p 6379:6379 redis:7-alpine`) and re-export.
