package denylist

import "regexp"

// pattern is a single deny-list entry. The deny-list is data, not code:
// new patterns are added by appending to the patterns slice below — never
// by adding inline if/else in Contains.
//
// The ID is opaque and stable; it appears in security event logs but is
// NEVER surfaced to end users (per ARCHITECTURE.md §9 Step 5: "deny-list
// hit → silent — do not signal which pattern was caught").
type pattern struct {
	id          string
	description string
	re          *regexp.Regexp
}

// Command-boundary prefix. A "command" is text that begins at the start
// of input, after a newline, after `;`, after `|` or `||`, after `&` or
// `&&`, after a backtick, or after `$(`. The prefix optionally consumes
// leading whitespace and a sudo/doas/env wrapper. This is what
// distinguishes a real command invocation from prose like "the rm -rf
// flag is dangerous". Shell line-continuations (`\` + newline) inside
// a command body are normalized to a single space by Contains before
// the regexes run, so no special handling is needed here.
//
// Captured-group-free so we can compose into larger expressions cheaply.
const cmdBoundary = `(?:^|[\n;&|` + "`" + `]|\$\()[ \t]*` +
	`(?:(?:sudo|doas|env|exec|nohup|time)\s+)*`

// mustCompile is a thin wrapper that panics at package init if a
// pattern fails to compile. Compilation happens exactly once.
func mustCompile(expr string) *regexp.Regexp {
	return regexp.MustCompile(expr)
}

// patterns is the deny-list. Order is significant only for the first-hit
// semantics of Contains: when multiple patterns match, the first listed
// wins. Keep related variants grouped under one ID where possible.
var patterns = []pattern{
	// rm -rf and flag-reorder variants. Matches `rm -rf`, `rm -fr`,
	// `rm -r -f`, `rm -f -r`, `rm -Rf`, `rm --recursive --force`, etc.
	// Requires the command boundary so the prose "the rm -rf flag" is
	// NOT a hit.
	{
		id:          "rm-recursive-force",
		description: "rm with recursive + force flags (any order)",
		re: mustCompile(
			`(?i)` + cmdBoundary +
				`rm\s+` +
				`(?:-[a-zA-Z]*[rR][a-zA-Z]*[fF][a-zA-Z]*` +
				`|-[a-zA-Z]*[fF][a-zA-Z]*[rR][a-zA-Z]*` +
				`|-[rRfF]\s+-[rRfF]` +
				`|--recursive\s+--force` +
				`|--force\s+--recursive` +
				`|-[rR]\s+--force` +
				`|--recursive\s+-[fF]` +
				`|-[fF]\s+--recursive` +
				`|--force\s+-[rR]` +
				`)\b`,
		),
	},

	// DROP TABLE / DROP DATABASE / TRUNCATE TABLE — destructive SQL.
	// Case-insensitive. No command boundary required: a SQL fragment in
	// a suggestion is almost always a real instruction, and we want to
	// catch `... ; DROP TABLE users; --` style injections too.
	{
		id:          "sql-destructive",
		description: "destructive SQL (DROP TABLE/DATABASE, TRUNCATE TABLE)",
		re: mustCompile(
			`(?i)\b(?:drop\s+(?:table|database)|truncate\s+table)\b`,
		),
	},

	// git push --force / -f / --force-with-lease. Command boundary so
	// "use git push --force" in a code-review comment isn't flagged
	// when it appears mid-sentence — but `git push --force` on its
	// own line (the actual command form) is.
	{
		id:          "git-push-force",
		description: "git push with --force / -f / --force-with-lease",
		re: mustCompile(
			cmdBoundary +
				`git\s+push\s+(?:[^\n]*\s)?(?:--force(?:-with-lease)?|-f)\b`,
		),
	},

	// Fork bomb. Canonical form is `:(){:|:&};:` but tolerate inner
	// whitespace and quoting variants. Does not require a command
	// boundary — the literal sequence is unambiguous.
	{
		id:          "fork-bomb",
		description: "classic shell fork bomb",
		re: mustCompile(
			`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`,
		),
	},

	// dd writing to a block device — wipes the disk. Match dd with
	// of=/dev/sd*, /dev/nvme*, /dev/hd*, /dev/xvd*, /dev/disk*, or the
	// loopback raw device /dev/mapper/*. Reading from /dev/zero or
	// /dev/random and writing anywhere "real" is the foot-gun.
	{
		id:          "dd-to-block-device",
		description: "dd writing to a block device",
		re: mustCompile(
			`(?i)` + cmdBoundary +
				`dd\s+(?:[^\n]*\s)?of=/dev/(?:sd[a-z]|nvme\d+n\d+|hd[a-z]|xvd[a-z]|disk\d+|mapper/)`,
		),
	},

	// mkfs against a block device. Same device set as dd-to-block-device.
	// mkfs.ext4 /dev/sda is the canonical form.
	{
		id:          "mkfs-on-block-device",
		description: "mkfs against a block device",
		re: mustCompile(
			`(?i)` + cmdBoundary +
				`mkfs(?:\.[a-z0-9]+)?\s+(?:[^\n]*\s)?/dev/(?:sd[a-z]|nvme\d+n\d+|hd[a-z]|xvd[a-z]|disk\d+|mapper/)`,
		),
	},

	// chmod -R 777 / — opens up the entire filesystem. Match any chmod
	// with -R and the literal mode 777 targeting `/` (or `/*`, `/.`).
	{
		id:          "chmod-777-root",
		description: "chmod -R 777 against /",
		re: mustCompile(
			`(?i)` + cmdBoundary +
				`chmod\s+(?:-[a-zA-Z]*R[a-zA-Z]*|--recursive)\s+0?777\s+/(?:\s|$|\*|\.)`,
		),
	},

	// Pipe-to-shell: curl/wget piped into sh/bash/zsh/dash, with or
	// without `-c`. Catches both `curl ... | sh` and `wget ... | bash`.
	// Tolerates intermediate `tee /dev/null |` and flag positions.
	{
		id:          "pipe-to-shell",
		description: "curl/wget piped to a shell interpreter",
		re: mustCompile(
			`(?i)` + cmdBoundary +
				`(?:curl|wget|fetch)\s+[^\n]*\|\s*(?:sudo\s+)?(?:sh|bash|zsh|dash|ksh|fish)\b`,
		),
	},
}

// patternByID is built once at init for O(1) lookup. Used only by the
// internal test that asserts pattern IDs are stable.
var patternByID = func() map[string]*pattern {
	m := make(map[string]*pattern, len(patterns))
	for i := range patterns {
		m[patterns[i].id] = &patterns[i]
	}
	return m
}()
