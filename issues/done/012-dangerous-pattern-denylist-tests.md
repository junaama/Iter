---
type: AFK
depends-on:
  - 007-go-module-skeleton
---

## Parent PRD

`ARCHITECTURE.md` §9 Step 2 (dangerous-pattern deny-list) and `CLAUDE.md` "Locked invariants" — examples include `rm -rf`, `DROP TABLE`, `git push --force`. Deny-list hits are blocked silently in suggestion output and logged as a security event.

## What to build

A pure Go function `Contains(text string) -> (hit bool, pattern_id string)` in `internal/denylist` that returns whether a suggestion contains any dangerous pattern, plus an opaque pattern identifier for logging. The pattern itself must NOT be returned in user-visible output (per §9 Step 5: "deny-list hit → silent (do not signal which pattern was caught)").

The deny-list is data, not code: declare it in `internal/denylist/patterns.go` (or a YAML/JSON file embedded via `//go:embed`) as a list of `{id, regex, description}` entries. Starting set, all required:

- `rm -rf` (and variants: `rm -fr`, `rm -rf /`, with flags reordered)
- `DROP TABLE` / `DROP DATABASE` / `TRUNCATE TABLE` (case-insensitive)
- `git push --force` / `git push -f` / `git push --force-with-lease`
- `:(){:|:&};:` (fork bomb)
- `dd if=/dev/zero of=/dev/sd*`
- `mkfs.` against block devices
- `chmod -R 777 /`
- `curl ... | sh` / `wget ... | bash` (pipe-to-shell)

TDD coverage:

1. **Positive tests**: each pattern in the deny-list has at least three positive matches (the canonical form + two variants).
2. **Negative tests**: benign strings containing partial fragments (e.g. `# the rm -rf flag is dangerous` in a comment context — decide: is this a hit? Document the policy and test accordingly).
3. **Bypass variants** (per §9 Step 6 security tests): unicode lookalikes, mixed-case where regex should be case-insensitive, embedded whitespace, line-continuation backslashes. These should match or not per your policy — document and test.
4. **Silent-output invariant**: the function returns `(hit, pattern_id)` — the pattern_id is for logs only. A separate test confirms that the public suggestion-output path (when wired up later) does not surface the pattern_id.
5. **Performance**: regex compilation happens once at package init; classification of 10KB of text completes in under 1ms (benchmark, not assertion).

## Acceptance criteria

- [ ] `internal/denylist.Contains` exists with documented signature
- [ ] Deny-list declared as data (slice literal or embedded file), not as inline `if/else`
- [ ] Every pattern listed above has at least three positive test cases
- [ ] Negative tests for benign partial matches; policy recorded in `DECISIONS.md`
- [ ] Bypass-variant tests for at least: case, whitespace, line continuations
- [ ] Regex compilation cached at package init (verified by benchmark — no per-call compile)
- [ ] Benchmark exists (`BenchmarkContains`) — under 1ms for 10KB input
- [ ] 100% coverage on `internal/denylist`
- [ ] `make test`, `make lint`, `make bench` (if added) pass

## Blocked by

- Blocked by `issues/007-go-module-skeleton.md`

## User stories addressed

Every `iter suggest` call passes through this. Underpins the §7 "post-suggestion-foot-gun" failure-mode guarantee and the security audit story.
