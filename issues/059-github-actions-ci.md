---
type: AFK
depends-on:
  - 048-cmd-server-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 3 ("GitHub repo with branch protection and CI (`go test` + `golangci-lint`)"). `testing.md` "CI gates" defines the merge requirements: `make test` passes, `golangci-lint run` passes, ≥1 human review, PR description includes "what changed" + "how to test".

## What to build

A GitHub Actions workflow that runs the full Go test + lint suite on every PR and on `main`, and the GitHub branch-protection settings that gate merges on it.

### AFK — workflow file

1. `.github/workflows/ci.yml` with two jobs:
   - **`lint`**: `actions/checkout@v4` → `actions/setup-go@v5` (Go 1.22+ per `DECISIONS.md`) → `golangci/golangci-lint-action@v6`. Cache modules + lint cache.
   - **`test`**: same Go setup → `make test`. The `make test` target already exists; it runs `go test ./...`. Testcontainers tests start their own Docker (Actions ships with Docker on `ubuntu-latest`); document the Docker requirement in the workflow comment.
2. Workflow triggers: `pull_request` for any branch targeting `main`; `push` to `main`.
3. Job timeout: 20 minutes (testcontainers cold-start can take 2-3 min).
4. Concurrency: cancel in-progress runs on the same PR when a new commit pushes — `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`.
5. Required env vars in the workflow: `CGO_ENABLED=0` for portability; `GOTOOLCHAIN=local` to pin the declared Go version.
6. Status check name must be stable (e.g. `ci / test`, `ci / lint`) so the branch-protection rule in the HITL step can reference it.
7. **Trufflehog install**: per `Makefile` `TRUFFLEHOG_VERSION := 3.95.3` (the redact tests assert against this). Install the pinned version in the `test` job before `make test`.

### HITL — branch protection (one-time)

1. On GitHub, enable branch protection on `main`:
   - Require pull request before merging
   - Require ≥1 approval
   - Require status checks: `ci / lint`, `ci / test`
   - Require branches to be up to date before merging
   - Require linear history
   - Restrict force-push
2. Record the configured rules in `deploy.md` ops section.

## Acceptance criteria

### AFK

- [ ] `.github/workflows/ci.yml` exists with `lint` + `test` jobs
- [ ] Both jobs run on PRs to `main` and on `main` pushes
- [ ] `make test` runs end-to-end in CI (testcontainers Docker available on the runner)
- [ ] Trufflehog 3.95.3 installed before `make test`
- [ ] Module + lint caches keyed on `go.sum`
- [ ] Concurrency cancels superseded runs
- [ ] Status check names stable

### HITL

- [ ] Branch protection enabled on `main` per the checklist
- [ ] Required checks reference the stable status names
- [ ] `deploy.md` records the rules

## Blocked by

- Blocked by `issues/048-cmd-server-skeleton.md`

(Soft-depends on `issues/in-progress/004-rls-cascade-delete-verification.md` — its `make test-rls` target, when it lands, should also be wired into the workflow. Not a hard blocker.)

## User stories addressed

Every contributor to the repo. Underpins the §9 Step 6 testing-gates story and the §7 reliability invariants — without CI, the locked invariants are documentation, not enforcement.
