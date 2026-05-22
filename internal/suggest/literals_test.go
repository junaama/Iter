package suggest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoThresholdLiteralsElsewhere enforces the "never reimplement" invariant
// from CLAUDE.md: the locked confidence thresholds (0.50, 0.80) MUST appear
// only inside internal/suggest. Any other occurrence under internal/ is a
// regression of the central decision-function contract.
//
// We parse Go source via go/ast (not grep) so we only match real float
// literals — not comments, not strings, not unrelated numerics. The scan
// covers all non-test .go files under internal/ except internal/suggest/
// itself (the one allowed home for the thresholds).
func TestNoThresholdLiteralsElsewhere(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	internalDir := filepath.Join(repoRoot, "internal")

	// Forbidden numeric values. Comparing parsed float64 (not strings) catches
	// "0.5", "0.50", "0.500", "5e-1" alike with no false positives from
	// unrelated tokens or comments.
	forbidden := map[float64]string{
		0.5: "0.50 (suppress→advisory threshold)",
		0.8: "0.80 (advisory→replace threshold)",
	}

	var offenders []string

	walkErr := filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if filepath.Clean(path) == filepath.Join(internalDir, "suggest") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.FLOAT {
				return true
			}
			raw := strings.ReplaceAll(lit.Value, "_", "")
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return true
			}
			if label, bad := forbidden[v]; bad {
				pos := fset.Position(lit.Pos())
				offenders = append(offenders,
					pos.String()+": forbidden threshold literal "+lit.Value+" — "+label)
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk internal/: %v", walkErr)
	}

	if len(offenders) > 0 {
		t.Fatalf("threshold literals leaked outside internal/suggest:\n  %s\n\nCall suggest.SuggestionAction instead of reimplementing the thresholds.",
			strings.Join(offenders, "\n  "))
	}
}

// findRepoRoot walks up from the test's working directory (which is the
// package dir, i.e. internal/suggest) looking for go.mod.
func findRepoRoot() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fs.ErrNotExist
		}
		dir = parent
	}
}
