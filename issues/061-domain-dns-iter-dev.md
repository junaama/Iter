---
type: HITL
depends-on:
  - 058-railway-staging-prod-environments
---

# HITL — requires domain registrar + DNS provider access

Registering / configuring `iter.dev` requires logging into a registrar (and possibly a DNS provider like Cloudflare). AFK workers should skip.

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("Domain DNS for iter.dev + staging.iter.dev"); `deploy.md` "Hosting targets": "Apex pointed at Railway; subdomains: staging.iter.dev, status.iter.dev."

## What to build

DNS for the production apex, the staging subdomain, and a placeholder for the status page. TLS is handled by Railway automatically once DNS resolves.

Specifically:

1. **Confirm registration**: `iter.dev` is acquired and renewed for ≥1 year. If not yet acquired, register it (this is the gate — without the domain, the rest of v1 doesn't ship under the right name).
2. **DNS provider**: Cloudflare is the recommended provider (free, simple, fast TTL changes). Document the choice in `deploy.md` if not Cloudflare.
3. **A/CNAME records**:
   - `iter.dev` apex → Railway production `iter-server` (per Railway's domain wizard, usually a CNAME to a Railway-provided hostname or an ALIAS/ANAME at the apex).
   - `staging.iter.dev` → Railway staging `iter-server`.
   - `status.iter.dev` → BetterStack status page (CNAME provided by BetterStack; placeholder OK if 062 not yet done).
4. **MX records**: deferred — no email at iter.dev domain yet; record an MX-related decision in `DECISIONS.md` if anything is set up (e.g. for `support@iter.dev`).
5. **TLS**: Railway provisions via Let's Encrypt automatically once DNS resolves. Verify with `curl -I https://staging.iter.dev/health` after propagation.
6. **DNS verification**: `dig iter.dev`, `dig staging.iter.dev`, `dig status.iter.dev` all resolve. Document the resolved record values in `deploy.md`.
7. **CAA records** (defense-in-depth): only allow Let's Encrypt to issue certs. `iter.dev. IN CAA 0 issue "letsencrypt.org"`.

## Acceptance criteria

- [ ] `iter.dev` registered and renewed ≥1 year out
- [ ] DNS provider chosen and recorded in `deploy.md`
- [ ] Apex `iter.dev` resolves to Railway production
- [ ] `staging.iter.dev` resolves to Railway staging
- [ ] `status.iter.dev` resolves (BetterStack target acceptable; placeholder OK if 062 not yet done)
- [ ] `https://staging.iter.dev/health` returns 200 with a valid TLS cert
- [ ] `https://iter.dev/health` returns 200 (or 404 if a marketing site is the apex — document the chosen behavior in `deploy.md`)
- [ ] CAA records pin Let's Encrypt
- [ ] `deploy.md` "Hosting targets" table updated with resolved record values

## Blocked by

- Blocked by `issues/058-railway-staging-prod-environments.md`

## User stories addressed

Every user hitting `iter.dev/download`, every `iter login` device-code redirect, every BetterStack uptime ping at `staging.iter.dev/health`.
