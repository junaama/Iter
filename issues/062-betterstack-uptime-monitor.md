---
type: HITL
depends-on:
  - 060-railway-cd-staging-prod
---

# HITL — requires BetterStack dashboard configuration

Creating the BetterStack account + monitor requires interactive dashboard work. AFK workers should skip.

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("BetterStack uptime monitor on staging `/health`"); §7 "Observability" — BetterStack is the single vendor for logs, metrics, uptime, status page, and on-call (per `DECISIONS.md` Phase 7); §7 "Minimum alerts" enumerates the alerts that land in Step 7 — this slice is the uptime monitor only.

## What to build

A BetterStack uptime monitor pointed at the Railway-generated staging `/health` URL, with email notifications to the founder. Full alerting (per `ARCHITECTURE.md` §7) lands in Step 7 — this slice is the foundation. Custom `staging.iter.dev` / `status.iter.dev` DNS is deferred to issue 061 and is not a blocker for this monitor.

Specifically:

1. **Account creation**: create a BetterStack account; configure billing if needed (free tier covers a single uptime monitor at 30s intervals).
2. **Uptime monitor**:
   - URL: Railway-generated staging domain from issue 060, `/health`
   - Expected status: `200`
   - Expected body contains: `"ok":true`
   - Interval: 30s (matches the Railway probe interval per `deploy.md` "Healthcheck")
   - Timeout: 5s
   - Regions: at least two (e.g. US-East + EU)
   - Alert after: 2 consecutive failures (avoids flapping)
3. **On-call**: email-only at v1 per `DECISIONS.md` Phase 7 ("Email notifications only (no SMS/PagerDuty for v1)"). Route to the founder.
4. **Status page**: provision BetterStack's hosted status page on its generated URL; map the staging `/health` monitor to a public "API" component. Custom `status.iter.dev` DNS lands later with issue 061. Production monitor + components land later (Step 7) when the prod `/health` is live.
5. **Source tokens**: capture `BETTERSTACK_SOURCE_TOKEN` (already enumerated in `deploy.md`) — used by the Step 7 logs/metrics integration.
6. **Integration test**: deliberately take the staging service down (e.g. pause it via Railway) and confirm the monitor turns red within 2 minutes and the email lands. Restore service. Document the drill in the PR.

## Acceptance criteria

- [ ] BetterStack account exists; billing configured per chosen tier
- [ ] Uptime monitor on the Railway-generated staging `/health` URL with 30s interval, 5s timeout, 2-region probing
- [ ] Body check on `"ok":true`
- [ ] Email-only alerting to founder
- [ ] BetterStack hosted status page exists on its generated URL; custom `status.iter.dev` remains deferred to 061
- [ ] Staging `/health` monitor mapped to a public component on the status page
- [ ] `BETTERSTACK_SOURCE_TOKEN` captured in Railway env vars
- [ ] Downtime drill: service paused → monitor red within 2 min → email received; documented in PR
- [ ] `deploy.md` first-deploy checklist updated to show the monitor live

## Blocked by

- Blocked by `issues/060-railway-cd-staging-prod.md`
- Custom DNS in `issues/deferred/061-domain-dns-iter-dev.md` is explicitly not a blocker.

## User stories addressed

Underpins the §7 "1h ack for P1" incident-response SLO. status.iter.dev is the user-visible commitment to transparency about outages.
