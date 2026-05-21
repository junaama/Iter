---
type: AFK
depends-on:
  - 007-go-module-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §5 "Confidence thresholds (locked)" and §9 Step 2. The function signature and behavior are already locked in `contracts.py:86-87` and `CLAUDE.md` "Locked invariants" — this slice mirrors them in Go and pins them with boundary tests.

## What to build

A pure Go function `suggestion_action(confidence float64, refined_prompt string) -> Action` in `internal/suggest` that implements the locked threshold contract:

- `confidence < 0.50` → `Suppress`
- `0.50 <= confidence < 0.80` → `Advisory` (return refined_prompt)
- `confidence >= 0.80` → `Replace` (return refined_prompt)

Per `CLAUDE.md`: "Clients call the pure `suggestion_action(confidence, refined_prompt)` decision function — never reimplement the thresholds elsewhere." So the function must be exported, the only place thresholds appear, and have a stable name.

TDD with **boundary tests at the exact threshold points**: `0.0`, `0.4999`, `0.50`, `0.7999`, `0.80`, `1.0`. Also: NaN handling (decide: error or `Suppress`?), negative values (error or `Suppress`?), `>1.0` values (error or treat as `1.0`?). Make a call, lock it in `DECISIONS.md`, and write the tests.

Bonus: a test that **fails** if anyone introduces a second hard-coded `0.5` or `0.8` literal elsewhere in `internal/` (grep-based test) — this enforces the "never reimplement" invariant.

## Acceptance criteria

- [ ] Function exported from `internal/suggest` (or `pkg/suggest` if it needs to be importable by daemon/CLI later — choose and document)
- [ ] Boundary tests at every threshold point (`0.0`, `<0.50`, `0.50`, `<0.80`, `0.80`, `1.0`)
- [ ] Out-of-band input handling (NaN, negative, `>1.0`) decided and tested
- [ ] No threshold literal (`0.5`, `0.80`, etc.) appears anywhere in `internal/` outside this file — enforced via test
- [ ] 100% coverage on `internal/suggest` for this function
- [ ] Decisions about NaN/out-of-range behavior recorded in `DECISIONS.md`
- [ ] `make test` and `make lint` pass

## Blocked by

- Blocked by `issues/007-go-module-skeleton.md`

## User stories addressed

Every `iter suggest` CLI invocation depends on this; dashboard "advisory vs replace" UI states depend on this.
