package denylist

import "strings"

// lineContinuationStripper folds shell line-continuations (`\` immediately
// before a newline) into a single space so a command split across physical
// lines still matches the canonical single-line pattern. Pre-normalizing
// here keeps every regex in patterns.go readable.
var lineContinuationStripper = strings.NewReplacer(
	"\\\r\n", " ",
	"\\\n", " ",
)

// Contains reports whether text matches any entry in the dangerous-pattern
// deny-list, returning the matched pattern's opaque ID for logging.
//
// Contract:
//   - The returned hit is true iff at least one deny-list pattern matched.
//   - patternID is non-empty exactly when hit is true; it is the id of the
//     first matching entry in the patterns slice.
//   - patternID is INTERNAL: it is intended for security event logs only
//     and MUST NOT be surfaced to end users. The suggestion-output path
//     wired up by issue 005 returns only the boolean and emits a generic
//     "suppressed" message — never the pattern id, regex, or description.
//   - Contains is pure: no I/O, no clocks, no globals other than the
//     compiled-at-init patterns slice.
//
// Performance:
//   - All regexes are compiled exactly once at package init via
//     regexp.MustCompile in patterns.go; Contains does no per-call
//     compilation.
//   - 10KB of input classifies in well under 1ms on commodity hardware
//     (see BenchmarkContains).
func Contains(text string) (hit bool, patternID string) {
	// Normalize line continuations only when they're present, to avoid
	// the alloc cost on the hot benign path.
	if strings.Contains(text, "\\\n") || strings.Contains(text, "\\\r\n") {
		text = lineContinuationStripper.Replace(text)
	}
	for i := range patterns {
		if patterns[i].re.MatchString(text) {
			return true, patterns[i].id
		}
	}
	return false, ""
}
