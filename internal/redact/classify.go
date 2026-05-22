// Package redact wraps trufflehog and a small in-tree PII detector to
// classify payloads as clean, strippable, or dirty before any cloud sync.
//
// The three-tier classification is a locked invariant per CLAUDE.md and
// ARCHITECTURE.md §3:
//
//   - Clean       — no findings
//   - Strippable  — findings present, but every finding can be redacted in
//     place; the returned bytes are safe to forward upstream.
//   - Dirty       — findings present that cannot be cleanly redacted, OR an
//     internal error occurred. Dirty payloads stay on-device.
//
// Per ARCHITECTURE.md §9 Step 5, trufflehog failure is fail-closed: any
// internal error (missing binary, non-zero exit, malformed output, timeout)
// returns Dirty. Errors are surfaced alongside the classification so the
// caller can log them, but the classification is authoritative.
package redact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Classification is the three-tier label assigned to a payload.
type Classification int

const (
	// Dirty is the zero value — fail-closed by construction. Any code path
	// that forgets to set a classification will mark the payload Dirty.
	Dirty Classification = iota
	Clean
	Strippable
)

// String returns the lowercase wire form of the classification.
func (c Classification) String() string {
	switch c {
	case Clean:
		return "clean"
	case Strippable:
		return "strippable"
	default:
		return "dirty"
	}
}

// Default subcommand timeout. Tests can override via Options.
const defaultTrufflehogTimeout = 30 * time.Second

// trufflehogBinary returns the name of the trufflehog executable to invoke.
// Tests can point this at a stub by setting ITER_TRUFFLEHOG_BIN.
func trufflehogBinary() string {
	if v := os.Getenv("ITER_TRUFFLEHOG_BIN"); v != "" {
		return v
	}
	return "trufflehog"
}

// Classify inspects payload, returning the assigned tier and (when the tier
// is Strippable) the redacted bytes that are safe to forward.
//
// Contract:
//   - Pure with respect to the (trufflehog binary version, payload, PII
//     policy) tuple: same inputs produce identical outputs across runs.
//   - On any internal error (binary missing, exec failure, malformed JSON,
//     timeout) the function returns (Dirty, payload, err) — fail-closed.
//     The returned bytes in the Dirty case are the original payload so
//     callers don't accidentally forward partially-modified data.
//   - For Clean payloads the returned bytes are the original payload.
//   - For Strippable payloads the returned bytes have every finding
//     replaced by a deterministic placeholder.
func Classify(payload []byte) (Classification, []byte, error) {
	return classifyWith(context.Background(), payload, defaultTrufflehogTimeout)
}

// classifyWith is the testable variant — takes a context + timeout so the
// trufflehog-failure test can force a short timeout if it ever needs to.
func classifyWith(ctx context.Context, payload []byte, timeout time.Duration) (Classification, []byte, error) {
	// 1. Detect PII first (in-memory regex). This is independent of
	//    trufflehog so we can still produce a stable result if trufflehog
	//    fails for unrelated reasons (we still fail-closed in that case —
	//    but PII findings on top of a trufflehog failure remain visible
	//    for logging).
	piiFindings := scanPII(payload)

	// 2. Shell out to trufflehog for secret detection.
	secretFindings, err := runTrufflehog(ctx, payload, timeout)
	if err != nil {
		// Fail-closed. Return the *original* payload so callers don't
		// forward a half-redacted buffer.
		return Dirty, payload, fmt.Errorf("trufflehog: %w", err)
	}

	// 3. Merge findings and decide tier.
	all := append([]finding(nil), piiFindings...)
	all = append(all, secretFindings...)

	if len(all) == 0 {
		return Clean, payload, nil
	}

	// 4. Attempt in-place redaction. Any finding flagged un-redactable
	//    forces Dirty.
	redacted, ok := redactAll(payload, all)
	if !ok {
		return Dirty, payload, nil
	}
	return Strippable, redacted, nil
}

// finding is the internal, detector-agnostic representation of a single
// match. Offsets are byte offsets into the original payload.
type finding struct {
	start       int
	end         int
	placeholder string
	// redactable is false for findings whose redaction would corrupt the
	// payload structurally — currently used for physical addresses (PII
	// policy: addresses are Dirty because masking them loses too much
	// context to be useful and is unreliable to detect).
	redactable bool
}

// ---------------------------------------------------------------------------
// PII detector
//
// Policy (recorded in DECISIONS.md):
//   - emails  → Strippable, replaced with [REDACTED_EMAIL]
//   - phones  → Strippable, replaced with [REDACTED_PHONE]
//   - addresses → Dirty (heuristic-only detector; no clean redaction)
//
// Names are NOT detected here — name detection from free text without an
// NLP model produces unacceptable false-positive rates against code.
// ---------------------------------------------------------------------------

var (
	// Conservative email pattern. Accepts the common case; deliberately
	// rejects display-name-style "Foo <a@b>" to keep the regex simple —
	// the inner address still matches.
	piiEmailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)

	// US-style phone numbers. Accepts:
	//   +1-555-123-4567, (555) 123-4567, 555-123-4567, 555.123.4567
	// Deliberately requires either separators or parens so we don't gobble
	// long runs of digits in log lines.
	piiPhoneRE = regexp.MustCompile(`(?:\+?1[\s\-.])?(?:\(\d{3}\)\s?|\d{3}[\s\-.])\d{3}[\s\-.]\d{4}\b`)

	// US street-address heuristic: digit run followed by a word and a
	// common suffix (Street, St, Avenue, Ave, Road, Rd, Boulevard, Blvd,
	// Drive, Dr, Lane, Ln, Way). Conservative — flags lines that look
	// address-shaped. Marks the payload Dirty (per policy above) rather
	// than attempting redaction.
	piiAddressRE = regexp.MustCompile(`(?i)\b\d{1,5}\s+[A-Za-z][A-Za-z0-9.\- ]{1,40}\s+(?:Street|St|Avenue|Ave|Road|Rd|Boulevard|Blvd|Drive|Dr|Lane|Ln|Way|Parkway|Pkwy|Court|Ct|Place|Pl)\b\.?`)
)

func scanPII(payload []byte) []finding {
	var out []finding
	for _, m := range piiEmailRE.FindAllIndex(payload, -1) {
		out = append(out, finding{start: m[0], end: m[1], placeholder: "[REDACTED_EMAIL]", redactable: true})
	}
	for _, m := range piiPhoneRE.FindAllIndex(payload, -1) {
		out = append(out, finding{start: m[0], end: m[1], placeholder: "[REDACTED_PHONE]", redactable: true})
	}
	for _, m := range piiAddressRE.FindAllIndex(payload, -1) {
		out = append(out, finding{start: m[0], end: m[1], placeholder: "[REDACTED_ADDRESS]", redactable: false})
	}
	return out
}

// ---------------------------------------------------------------------------
// trufflehog wrapper
// ---------------------------------------------------------------------------

// trufflehogResult mirrors the relevant fields of trufflehog's JSON output.
// Trufflehog emits one JSON object per line on stdout when invoked with
// --json. We only need the raw match string and detector name — enough to
// locate the byte offsets in the original payload and to produce a stable
// placeholder.
type trufflehogResult struct {
	DetectorName string `json:"DetectorName"`
	Raw          string `json:"Raw"`
	RawV2        string `json:"RawV2"`
	Verified     bool   `json:"Verified"`
}

func runTrufflehog(ctx context.Context, payload []byte, timeout time.Duration) ([]finding, error) {
	// Write payload to a tempfile in an empty tempdir so trufflehog's
	// filesystem source scans exactly one input. Using a directory rather
	// than a single file gives trufflehog a stable root to chdir into and
	// avoids edge cases around very small files.
	tmpDir, err := os.MkdirTemp("", "iter-redact-")
	if err != nil {
		return nil, fmt.Errorf("mktempdir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpPath := filepath.Join(tmpDir, "payload.txt")
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return nil, fmt.Errorf("write tempfile: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// --no-update      — never reach out for a self-update during a scan.
	// --no-verification — never make outbound HTTP calls to verify a hit.
	//                    Determinism + no side effects.
	// --json           — newline-delimited JSON output, one object per
	//                    finding.
	cmd := exec.CommandContext(cctx, trufflehogBinary(),
		"filesystem", tmpDir,
		"--json", "--no-update", "--no-verification",
	)
	// Inherit a minimal env; some trufflehog versions read HOME for cache.
	cmd.Env = append(os.Environ(), "TRUFFLEHOG_NO_UPDATE=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Distinguish exec failure from non-zero exit. Trufflehog's exit
		// code for a successful scan with findings is 0; non-zero
		// indicates an actual problem (binary missing, panic, etc.).
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timeout after %s", timeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("non-zero exit: %s: %s", ee.String(), truncate(stderr.String(), 256))
		}
		return nil, fmt.Errorf("exec: %w: %s", err, truncate(stderr.String(), 256))
	}

	return parseTrufflehogOutput(payload, &stdout)
}

func parseTrufflehogOutput(payload []byte, r io.Reader) ([]finding, error) {
	out := []finding{}
	dec := json.NewDecoder(r)
	// Trufflehog emits NDJSON. Loop until EOF; tolerate intermixed blank
	// lines by reading tokens defensively.
	for {
		var res trufflehogResult
		if err := dec.Decode(&res); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode finding: %w", err)
		}
		raw := res.Raw
		if raw == "" {
			raw = res.RawV2
		}
		if raw == "" {
			// Defensive: a finding without a Raw match is unusable.
			continue
		}
		// Locate every occurrence of the raw string in the payload.
		// Trufflehog reports each finding individually, but a single
		// secret may appear multiple times in the file — covering every
		// occurrence keeps the redaction complete.
		for _, off := range allIndex(payload, []byte(raw)) {
			out = append(out, finding{
				start:       off,
				end:         off + len(raw),
				placeholder: "[REDACTED_" + sanitizeDetector(res.DetectorName) + "]",
				redactable:  true,
			})
		}
	}
	return out, nil
}

// allIndex returns every start offset of needle in haystack (non-overlapping).
func allIndex(haystack, needle []byte) []int {
	if len(needle) == 0 {
		return nil
	}
	var offs []int
	start := 0
	for start <= len(haystack)-len(needle) {
		i := bytes.Index(haystack[start:], needle)
		if i < 0 {
			break
		}
		offs = append(offs, start+i)
		start += i + len(needle)
	}
	return offs
}

func sanitizeDetector(name string) string {
	if name == "" {
		return "SECRET"
	}
	// Uppercase ASCII letters/digits only. Keeps placeholders stable and
	// avoids leaking detector-internal punctuation into the wire format.
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "SECRET"
	}
	return b.String()
}

// truncate returns s clipped to n bytes, with an ellipsis if clipped.
// Used to keep error strings bounded.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------------------------------------------------------------------------
// redaction
// ---------------------------------------------------------------------------

// redactAll rewrites payload with every finding's range replaced by its
// placeholder. Returns (redactedBytes, true) on success. Returns (nil,
// false) if any finding is flagged non-redactable (forces Dirty), or if
// findings overlap in an unresolvable way.
//
// Findings are deduplicated by (start,end) and processed in start-ascending
// order. Overlapping ranges that share a placeholder are merged; mismatched
// overlaps return false.
func redactAll(payload []byte, findings []finding) ([]byte, bool) {
	if len(findings) == 0 {
		return payload, true
	}

	// Any non-redactable finding → Dirty.
	for _, f := range findings {
		if !f.redactable {
			return nil, false
		}
	}

	// Sort by (start, end). Stable.
	sorted := append([]finding(nil), findings...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].start != sorted[j].start {
			return sorted[i].start < sorted[j].start
		}
		return sorted[i].end < sorted[j].end
	})

	// Merge overlaps, keeping the longest range. If two overlapping
	// findings disagree on placeholder, we widen to a generic
	// [REDACTED_OVERLAP] marker — deterministic, never lossy in a way
	// that would let a secret leak.
	merged := []finding{sorted[0]}
	for _, f := range sorted[1:] {
		last := &merged[len(merged)-1]
		if f.start < last.end {
			// Overlap.
			if f.end > last.end {
				last.end = f.end
			}
			if f.placeholder != last.placeholder {
				last.placeholder = "[REDACTED_OVERLAP]"
			}
			continue
		}
		merged = append(merged, f)
	}

	// Build output.
	var buf bytes.Buffer
	cursor := 0
	for _, f := range merged {
		if f.start < cursor || f.end > len(payload) || f.start > f.end {
			// Defensive: shouldn't happen after merging, but a bad
			// finding range means we can't trust the redaction —
			// fail-closed.
			return nil, false
		}
		buf.Write(payload[cursor:f.start])
		buf.WriteString(f.placeholder)
		cursor = f.end
	}
	buf.Write(payload[cursor:])
	return buf.Bytes(), true
}
