---
type: AFK
depends-on:
  - 007-go-module-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §3 "Pre-ingestion redaction" and §9 Step 2 (classification function). Three-tier classification (`clean | strippable | dirty`) is a locked invariant per `CLAUDE.md`.

## What to build

A pure-wrapper Go function `Classify(payload []byte) -> Classification` in `internal/redact` that shells out to `trufflehog` (or invokes it via Go module if a stable one exists) and returns one of `clean | strippable | dirty`:

- `clean` — no findings
- `strippable` — findings present but every finding can be redacted in place (the function also returns the redacted bytes)
- `dirty` — findings that cannot be cleanly redacted (verifier confirms a real secret OR redaction would corrupt the payload); payload stays on-device

The function is "pure" in the sense that for a fixed trufflehog version + corpus, same input → same output. Determinism is part of the contract.

TDD with three corpora committed under `testdata/`:

1. **Secrets corpus** — real-shaped fake secrets (AWS keys, GitHub tokens, Stripe keys, JWTs, generic high-entropy strings). Expected: `strippable` (redactable) or `dirty` (verified live — but use fakes, so should be `strippable`).
2. **PII corpus** — names, emails, phone numbers, addresses. Expected: per the redaction policy in `ARCHITECTURE.md` §3 — codify it here, decide and record if undecided.
3. **Clean corpus** — code samples, log lines, prose with no findings. Expected: `clean`.

Plus:

- **Idempotency**: `Classify(Classify(redacted))` returns `clean` (re-running on already-redacted output produces no new findings).
- **Determinism**: 100 runs of the same input produce identical output (classification + redacted bytes).
- **Trufflehog failure**: per §9 Step 5 "trufflehog failure (fail-closed)" — if the binary errors or times out, return `dirty`. Test with a forced failure (e.g. `PATH` manipulation in the test).

## Acceptance criteria

- [ ] `internal/redact.Classify` exists with documented signature
- [ ] `testdata/secrets/`, `testdata/pii/`, `testdata/clean/` corpora committed
- [ ] Tests cover all three corpora with expected classifications
- [ ] Idempotency test passes (redacted output classifies as `clean`)
- [ ] Determinism test passes (100 runs, identical output)
- [ ] Trufflehog-failure test passes (fail-closed → `dirty`)
- [ ] Trufflehog binary version pinned (in Makefile, `tools.go`, or a `trufflehog.version` file) and documented
- [ ] PII redaction policy recorded in `DECISIONS.md` if it wasn't already decided
- [ ] `make test` and `make lint` pass

## Blocked by

- Blocked by `issues/007-go-module-skeleton.md`

## User stories addressed

Every ingestion path depends on this — Adam's traces, Linear webhooks, every `iter suggest` invocation that sends context to the cloud.
