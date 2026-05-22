---
type: AFK
depends-on:
  - 029-middleware-scaffold
  - 017-repositories-scoring-outcomes
---

## Parent PRD

`CLAUDE.md` "Locked invariants" — **HMAC verification required on webhooks**, **Idempotency-Key required on all POST endpoints**. `ARCHITECTURE.md` §5 "Webhooks at v1: GitHub + Linear" + §7 "Webhook delivery delayed."

## What to build

`POST /v1/webhooks/github` — receives `pull_request`, `push`, and `check_run` events from GitHub. Maps them to `outcomes` rows on the matching session. Public endpoint (no JWT auth); HMAC-verified.

Pipeline:

1. **HMAC verify** — `X-Hub-Signature-256` header against `GITHUB_WEBHOOK_SECRET`. Constant-time compare. Bad signature → 401, audit-log entry `webhook_signature_failed`. Per `ARCHITECTURE.md` §7, do NOT leak which check failed.
2. **Idempotency** — GitHub sends `X-GitHub-Delivery` (UUID). Use that as the Idempotency-Key. Duplicate delivery: no-op, return 200 with cached response.
3. **Event routing** — dispatch by `X-GitHub-Event` header:
   - `pull_request` with action `closed` + `merged=true` → upsert `outcomes(outcome_type='pr_merged', external_ref=<pr_url>)`
   - `pull_request` with action `closed` + `merged=false` and a label containing `revert` OR title matching `^Revert ` → `pr_reverted`
   - `push` with commits → `commit_landed` for each commit-message-mentioned session_id (parse `Closes session: <uuid>` markers we'll define in DECISIONS.md)
   - `check_run` with conclusion `success` → `tests_passed`; `failure` → `tests_failed`
4. **Session matching** — webhook events reference repos / PRs / commits. The matching strategy is by `repo_hash` (sha256 of the GitHub repo URL) + commit SHA or PR number. If no session matches: hold the event in a `pending_outcomes` table for 7 days; retry session-lookup nightly. Per `ARCHITECTURE.md` §9 Step 5 "Webhook edge cases."
5. **Response** — always 200 unless signature failed (401) or body is malformed (400). GitHub retries 5xx; we never want that for soft-misses.

## Acceptance criteria

- [ ] Endpoint registered as PUBLIC (no JWT auth), but with HMAC verification IN-handler (or via a webhook-specific middleware shared between GitHub + Linear)
- [ ] HMAC verification uses constant-time compare; bad signature → 401 with audit-log entry
- [ ] Idempotency keyed by `X-GitHub-Delivery`; duplicate delivery returns cached response
- [ ] Each event type maps correctly per the routing table above
- [ ] `pending_outcomes` table either exists (in 0001_initial) or is added in a new migration; if added, RLS + cascade-delete invariants are honored
- [ ] Commit-message session marker format documented in `DECISIONS.md`
- [ ] Integration tests with fixture payloads from real GitHub deliveries; test the full matrix of event/action combinations
- [ ] `make test` + `make test-rls` + `make lint` pass

## Blocked by

- Blocked by `issues/029-middleware-scaffold.md`
- Blocked by Step 3 storage-layer baseline — outcomes + audit_log repository functions

## User stories addressed

Session outcomes auto-attach as PRs merge / revert / fail tests. No manual labeling.
