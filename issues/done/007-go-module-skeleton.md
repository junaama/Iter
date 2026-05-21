---
type: AFK
depends-on: []
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 2 (preamble) and Step 3 ("Repository structure"). The full repo layout lands in Step 3, but Step 2 cannot start writing tests without a Go module to put them in. This slice is the minimal precursor.

## What to build

A minimal Go module skeleton that lets every other Step 2 slice write pure-function tests in parallel. NOT the full Step 3 layout — just enough to compile and `go test ./...`.

Specifically:

1. `go.mod` at repo root declaring the module path (suggest `github.com/<org>/iter`; if the org is undecided, use a placeholder like `github.com/iter-dev/iter` and note it in `DECISIONS.md`).
2. Empty package stubs under `internal/` for each domain that Step 2 needs: `internal/scoring`, `internal/suggest`, `internal/redact`, `internal/signals`, `internal/denylist`. One `doc.go` per package with a single sentence describing the domain.
3. A `Makefile` (or `Taskfile`, pick one and record in DECISIONS.md) with a `test` target that runs `go test ./...`.
4. `.golangci.yml` with a minimum-viable lint config (gofmt, govet, staticcheck, errcheck). Don't tune it now — the bar is "lints exist."
5. `go test ./...` passes (zero tests yet, but exits 0). `golangci-lint run` passes.

Do NOT add `cmd/server`, `internal/db`, `internal/api`, etc. — those are Step 3.

## Acceptance criteria

- [ ] `go.mod` exists with Go 1.22+ declared
- [ ] Module path chosen and recorded in `DECISIONS.md`
- [ ] Five package stubs exist under `internal/` with `doc.go` each
- [ ] `Makefile` (or `Taskfile`) with `test` and `lint` targets
- [ ] `.golangci.yml` present
- [ ] `make test` passes (or `task test`)
- [ ] `make lint` passes
- [ ] CI configuration NOT added here (lands in Step 3) — just the local targets

## Blocked by

None — can start immediately.

## User stories addressed

Foundational; enables every Step 2 test slice (008–012).
