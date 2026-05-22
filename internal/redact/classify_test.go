package redact_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iter-dev/iter/internal/redact"
)

// requireTrufflehog skips the test if the trufflehog binary isn't on PATH.
// Issue 010 pins the binary version (see trufflehog.version at repo root);
// CI is expected to install it. Local runs that lack it skip explicitly so
// `make test` still passes on a bare laptop.
func requireTrufflehog(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("trufflehog"); err != nil {
		t.Skip("trufflehog not on PATH; install via `brew install trufflehog` (see trufflehog.version)")
	}
}

func readCorpus(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read corpus %s: %v", rel, err)
	}
	return b
}

// listCorpus returns all regular files under testdata/<group>/.
func listCorpus(t *testing.T, group string) []string {
	t.Helper()
	dir := filepath.Join("testdata", group)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus dir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	if len(out) == 0 {
		t.Fatalf("corpus group %s is empty", group)
	}
	return out
}

// ---------------------------------------------------------------------------
// Corpus-driven classification tests
// ---------------------------------------------------------------------------

func TestClassify_CleanCorpus(t *testing.T) {
	requireTrufflehog(t)
	for _, path := range listCorpus(t, "clean") {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			payload := readCorpus(t, path)
			tier, out, err := redact.Classify(payload)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tier != redact.Clean {
				t.Errorf("got %s, want clean", tier)
			}
			if !bytes.Equal(out, payload) {
				t.Errorf("clean payload should be returned unchanged")
			}
		})
	}
}

func TestClassify_SecretsCorpus(t *testing.T) {
	requireTrufflehog(t)
	for _, path := range listCorpus(t, "secrets") {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			payload := readCorpus(t, path)
			tier, out, err := redact.Classify(payload)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tier != redact.Strippable {
				t.Fatalf("got %s, want strippable (trufflehog should detect the fake secret)", tier)
			}
			if bytes.Equal(out, payload) {
				t.Errorf("expected redacted output to differ from input")
			}
			if !bytes.Contains(out, []byte("[REDACTED_")) {
				t.Errorf("expected redaction marker in output, got: %s", truncateForLog(out))
			}
		})
	}
}

func TestClassify_PIICorpus(t *testing.T) {
	requireTrufflehog(t)

	// Per the policy recorded in DECISIONS.md (issue 010):
	//   - email_phone.txt → Strippable (emails + phones are redactable)
	//   - address.txt     → Dirty       (addresses force on-device only)
	want := map[string]redact.Classification{
		"email_phone.txt": redact.Strippable,
		"address.txt":     redact.Dirty,
	}
	for _, path := range listCorpus(t, "pii") {
		path := path
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			expected, ok := want[name]
			if !ok {
				t.Fatalf("no expected classification for %s — extend the test table", name)
			}
			payload := readCorpus(t, path)
			tier, out, err := redact.Classify(payload)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tier != expected {
				t.Fatalf("got %s, want %s", tier, expected)
			}
			switch expected {
			case redact.Strippable:
				if !bytes.Contains(out, []byte("[REDACTED_EMAIL]")) && !bytes.Contains(out, []byte("[REDACTED_PHONE]")) {
					t.Errorf("expected at least one PII redaction marker")
				}
				if bytes.Contains(out, []byte("@example.com")) || bytes.Contains(out, []byte("@example.org")) {
					t.Errorf("email leaked past redaction: %s", truncateForLog(out))
				}
			case redact.Dirty:
				// Dirty payloads must return the *original* bytes so a
				// caller can't accidentally forward partially-redacted
				// data upstream.
				if !bytes.Equal(out, payload) {
					t.Errorf("dirty payload must return original bytes unchanged")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Idempotency: re-classifying redacted output produces Clean.
// ---------------------------------------------------------------------------

func TestClassify_Idempotent_OnStrippablePayloads(t *testing.T) {
	requireTrufflehog(t)
	// Every Strippable corpus file: redact it, then re-classify the
	// redacted output. Expectation: Clean with no further changes.
	for _, group := range []string{"secrets"} {
		for _, path := range listCorpus(t, group) {
			path := path
			t.Run(filepath.Base(path), func(t *testing.T) {
				payload := readCorpus(t, path)
				tier1, out1, err := redact.Classify(payload)
				if err != nil {
					t.Fatalf("first pass: %v", err)
				}
				if tier1 != redact.Strippable {
					t.Fatalf("first pass tier = %s, want strippable", tier1)
				}
				tier2, out2, err := redact.Classify(out1)
				if err != nil {
					t.Fatalf("second pass: %v", err)
				}
				if tier2 != redact.Clean {
					t.Fatalf("re-classifying redacted output gave %s, want clean", tier2)
				}
				if !bytes.Equal(out2, out1) {
					t.Errorf("second pass changed the bytes — redaction is not a fixpoint")
				}
			})
		}
	}
}

func TestClassify_Idempotent_OnPIIStrippable(t *testing.T) {
	requireTrufflehog(t)
	payload := readCorpus(t, filepath.Join("testdata", "pii", "email_phone.txt"))
	tier1, out1, err := redact.Classify(payload)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if tier1 != redact.Strippable {
		t.Fatalf("first pass tier = %s, want strippable", tier1)
	}
	tier2, out2, err := redact.Classify(out1)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if tier2 != redact.Clean {
		t.Fatalf("second pass tier = %s, want clean", tier2)
	}
	if !bytes.Equal(out2, out1) {
		t.Errorf("second pass changed redacted bytes")
	}
}

// ---------------------------------------------------------------------------
// Determinism: 100 runs of the same input produce identical output.
// ---------------------------------------------------------------------------

func TestClassify_Deterministic(t *testing.T) {
	requireTrufflehog(t)
	// Use one representative from each tier so determinism is asserted
	// across the full surface of the function.
	cases := []string{
		filepath.Join("testdata", "secrets", "aws_access_key.txt"),
		filepath.Join("testdata", "pii", "email_phone.txt"),
		filepath.Join("testdata", "clean", "logs.txt"),
	}
	// Each Classify call spawns trufflehog (~1s). 100 runs × 3 cases = ~5min
	// — too slow for `make test`. 10 runs × 3 cases still gives 30 trufflehog
	// invocations against three tiers, which is enough to catch determinism
	// regressions without making the test suite painful. Use `go test -count`
	// to multiply if a deeper sweep is needed.
	const runs = 10
	for _, path := range cases {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			payload := readCorpus(t, path)
			firstTier, firstOut, err := redact.Classify(payload)
			if err != nil {
				t.Fatalf("baseline: %v", err)
			}
			for i := 0; i < runs; i++ {
				tier, out, err := redact.Classify(payload)
				if err != nil {
					t.Fatalf("run %d: %v", i, err)
				}
				if tier != firstTier {
					t.Fatalf("run %d: tier %s, want %s", i, tier, firstTier)
				}
				if !bytes.Equal(out, firstOut) {
					t.Fatalf("run %d: bytes changed", i)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Fail-closed: when trufflehog cannot run, classify returns Dirty.
// ---------------------------------------------------------------------------

func TestClassify_FailClosed_OnTrufflehogFailure(t *testing.T) {
	// Build a stub script that always exits non-zero, point
	// ITER_TRUFFLEHOG_BIN at it, and confirm the result is Dirty.
	if runtime.GOOS == "windows" {
		t.Skip("shell stub uses POSIX sh; skipped on Windows")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "trufflehog-stub.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'forced failure' >&2\nexit 17\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("ITER_TRUFFLEHOG_BIN", stub)

	// A clearly-clean payload — if fail-closed isn't honored this would
	// be classified Clean.
	payload := []byte("hello world, nothing to see here\n")
	tier, out, err := redact.Classify(payload)
	if tier != redact.Dirty {
		t.Errorf("got %s, want dirty (fail-closed)", tier)
	}
	if !bytes.Equal(out, payload) {
		t.Errorf("dirty payload must return original bytes unchanged")
	}
	if err == nil {
		t.Errorf("expected an error alongside the dirty classification")
	}
}

func TestClassify_FailClosed_OnMissingBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX path manipulation only")
	}
	// Point at a binary that doesn't exist.
	t.Setenv("ITER_TRUFFLEHOG_BIN", filepath.Join(t.TempDir(), "definitely-not-here"))

	tier, out, err := redact.Classify([]byte("anything"))
	if tier != redact.Dirty {
		t.Errorf("got %s, want dirty (fail-closed on missing binary)", tier)
	}
	if !bytes.Contains(out, []byte("anything")) {
		t.Errorf("expected original bytes back, got %q", out)
	}
	if err == nil {
		t.Errorf("expected an error")
	}
}

// ---------------------------------------------------------------------------
// Version-pin assertion: the binary on PATH must match trufflehog.version.
// ---------------------------------------------------------------------------

func TestTrufflehogBinary_VersionMatchesPin(t *testing.T) {
	requireTrufflehog(t)
	pinned, err := os.ReadFile(filepath.Join("..", "..", "trufflehog.version"))
	if err != nil {
		t.Fatalf("read trufflehog.version: %v", err)
	}
	want := strings.TrimSpace(string(pinned))
	if want == "" {
		t.Fatal("trufflehog.version is empty")
	}
	out, err := exec.Command("trufflehog", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("trufflehog --version: %v: %s", err, out)
	}
	// Trufflehog prints something like "trufflehog 3.95.3".
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, want) {
		t.Errorf("trufflehog version mismatch: got %q, want pin %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func truncateForLog(b []byte) string {
	const n = 200
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
