package archive

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"

	"github.com/iter-dev/iter/internal/db/repo"
)

// TestObjectKey asserts the deterministic R2 layout. The test is the
// codification of the deploy.md "Archive layout" contract — a change
// to the key shape is an operationally-visible event.
func TestObjectKey(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	sessionID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	startedAt := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	b := SessionBundle{
		Session: repo.Session{
			ID:        sessionID,
			TenantID:  tenantID,
			StartedAt: startedAt,
		},
	}
	got := b.ObjectKey()
	want := "11111111-1111-1111-1111-111111111111/2026-05/22222222-2222-2222-2222-222222222222.tar.zst"
	if got != want {
		t.Fatalf("ObjectKey() = %q, want %q", got, want)
	}
}

// TestEncodeTarZstd_RoundTrip serializes a non-empty bundle, then zstd-
// decompresses + tar-extracts to verify every expected entry is present
// and contains JSON.
func TestEncodeTarZstd_RoundTrip(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	sessionID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	startedAt := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	b := SessionBundle{
		Session: repo.Session{
			ID:             sessionID,
			TenantID:       tenantID,
			Harness:        "claude_code",
			Model:          "m",
			StartedAt:      startedAt,
			RedactedPrompt: "p",
			Classification: repo.ClassificationClean,
		},
		Events:    []repo.SessionEventRow{{ID: 7, SessionID: sessionID, TenantID: tenantID}},
		Scores:    []repo.Score{{SessionID: sessionID, TenantID: tenantID, ScorerVersion: "v"}},
		Outcomes:  []repo.Outcome{{SessionID: sessionID, TenantID: tenantID, OutcomeType: "tests_passed"}},
		BundledAt: startedAt,
	}

	out, err := EncodeTarZstd(b)
	if err != nil {
		t.Fatalf("EncodeTarZstd: %v", err)
	}

	// zstd-decompress
	zr, err := zstd.NewReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("zstd read: %v", err)
	}

	// tar-extract
	tr := tar.NewReader(bytes.NewReader(raw))
	seen := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar body %s: %v", hdr.Name, err)
		}
		seen[hdr.Name] = string(body)
	}

	for _, name := range []string{"session.json", "events.json", "scores.json", "outcomes.json"} {
		if _, ok := seen[name]; !ok {
			t.Errorf("tar entry %q missing", name)
		}
	}
	if _, ok := seen["embedding.json"]; ok {
		t.Error("embedding.json present even though Embedding was nil")
	}
	// Spot-check that session.json mentions the harness.
	if !strings.Contains(seen["session.json"], "claude_code") {
		t.Errorf("session.json did not contain harness; got: %s", seen["session.json"])
	}
}

// TestUsage_MaxFrac asserts the guardrail picks the highest of the
// three metrics. Belt-and-braces because a future addition of a fourth
// metric (egress) must update both the struct and MaxFrac in lockstep.
func TestUsage_MaxFrac(t *testing.T) {
	cases := []struct {
		name string
		u    Usage
		want float64
	}{
		{"all zero", Usage{}, 0},
		{"storage dominant", Usage{StorageFrac: 0.9, ClassAFrac: 0.1, ClassBFrac: 0.1}, 0.9},
		{"class A dominant", Usage{StorageFrac: 0.1, ClassAFrac: 0.95, ClassBFrac: 0.5}, 0.95},
		{"class B dominant", Usage{StorageFrac: 0.1, ClassAFrac: 0.5, ClassBFrac: 0.99}, 0.99},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.u.MaxFrac(); got != c.want {
				t.Errorf("MaxFrac() = %.3f, want %.3f", got, c.want)
			}
		})
	}
}
