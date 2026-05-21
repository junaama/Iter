---
type: AFK
depends-on:
  - 007-go-module-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 2 (signal aggregation) and §3 "Tables" (`session_events` → `ScoreSignals`). See `contracts.py` `ScoreSignals` for the output shape.

## What to build

A pure Go function `Aggregate(events []SessionEvent) -> ScoreSignals` in `internal/signals` that folds a list of session events into a single `ScoreSignals` value. This is the bridge between raw ingested events and the scoring function in issue 008.

The function must be:

- **Order-independent**: shuffling the input list produces the same output. Events arrive out of order over WebSocket; aggregation must not care.
- **Idempotent on duplicates**: appending a duplicate event (same event id) produces the same output. Replay scenarios are explicit in `ARCHITECTURE.md` §9 Step 5 ("replay (upsert)").
- **Pure**: no I/O, no clocks (any timestamps come from the events themselves).

TDD with these test groups:

1. **Basic aggregation**: known event lists → expected signals (table-driven).
2. **Order independence**: `Aggregate(events)` == `Aggregate(shuffle(events))` for randomly shuffled inputs (property test, run N=1000 shuffles).
3. **Duplicate idempotency**: `Aggregate(events ++ events)` == `Aggregate(events)`. Use event id as the dedup key.
4. **Empty input**: `Aggregate([])` returns a zero-value `ScoreSignals` (no panic, no error).
5. **Single event**: edge case — works correctly with one input.
6. **Subagent independence** (per §9 Step 5): subagent events are aggregated independently from the parent session's signals. Test this explicitly.

## Acceptance criteria

- [ ] `internal/signals.Aggregate` function exists with documented signature
- [ ] Table-driven tests pass for the basic-aggregation corpus
- [ ] Order-independence property test passes with N≥1000 shuffles
- [ ] Duplicate-idempotency test passes
- [ ] Empty-input and single-event edge cases pass
- [ ] Subagent independence test passes
- [ ] `SessionEvent` Go type added to `pkg/contracts` mirroring whatever Python equivalent exists (or newly defined here; record in DECISIONS.md if so)
- [ ] 100% coverage on `internal/signals`
- [ ] `make test` and `make lint` pass

## Blocked by

- Blocked by `issues/007-go-module-skeleton.md`

## User stories addressed

Feeds the scoring function (008); every dashboard score and every `iter suggest` "no_evidence / low_confidence" determination depends on aggregation being correct.
