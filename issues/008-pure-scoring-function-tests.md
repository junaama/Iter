---
type: AFK
depends-on:
  - 007-go-module-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 2 (pure scoring function). See also §3 Tables (`scores`) and `contracts.py` `ScoreSignals` / `Score` types for the input/output shape.

## What to build

A pure Go function in `internal/scoring` that takes a `ScoreSignals` input and returns a deterministic `Score` output (the per-session score persisted to the `scores` table). "Pure" means: no I/O, no clocks, no randomness, no globals — same input → same output, every time.

Implementation is TDD per `scripts/prompt.md` — tests first. Cover:

- **Table-driven tests**: a corpus of `(ScoreSignals → expected Score)` cases hand-curated to lock in the scoring contract. At minimum: zero-signal case, max-signal case, mixed-signal case, edge cases per signal field.
- **Property tests** (e.g. via `testing/quick` or `gopter`): monotonicity (more positive signals never decrease score), boundedness (score always within declared range), determinism (re-running with same input is identical).
- **Ordering independence**: if the function consumes a list anywhere, shuffling it must not change output.

Mirror the relevant fields from `contracts.py` `ScoreSignals` in Go (`pkg/contracts`). Per `CLAUDE.md`, `ScoreSignals` uses `extra="allow"` because signals evolve — the Go mirror should accept unknown fields without erroring (e.g. a `map[string]any` for extension fields).

## Acceptance criteria

- [ ] Tests written before implementation (TDD)
- [ ] `internal/scoring` package contains the pure function with a documented signature
- [ ] Table-driven tests cover zero / max / mixed / per-field edge cases
- [ ] Property tests cover monotonicity, boundedness, determinism, ordering independence
- [ ] 100% line coverage on `internal/scoring` (this is a pure function — there's no excuse for less)
- [ ] `pkg/contracts` Go types added matching `contracts.py` `ScoreSignals` and `Score`
- [ ] `make test` passes; `make lint` passes
- [ ] If the scoring formula required a decision not already in `ARCHITECTURE.md` or `contracts.py`, record it in `DECISIONS.md`

## Blocked by

- Blocked by `issues/007-go-module-skeleton.md`

## User stories addressed

Underpins the dashboard score displays (Me, Team) and the nightly scoring batch.
