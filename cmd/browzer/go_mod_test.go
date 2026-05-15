package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/testutil"
)

// TestGoModHasNoCuelangDependency pins the absence of cuelang.org/go
// from packages/cli/go.mod. The CUE schema toolchain was removed when
// workflow.json was retired; reintroducing the dependency would resurrect
// the dead path. The check scans line-by-line so that a commented-out
// hypothetical mention (e.g. in a future migration note) would not trip
// it, while any uncommented require entry does.
func TestGoModHasNoCuelangDependency(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	goModPath := filepath.Join(repoRoot, "packages", "cli", "go.mod")

	f, err := os.Open(goModPath)
	if err != nil {
		t.Fatalf("open go.mod at %s: %v", goModPath, err)
	}
	defer f.Close()

	// Match a real require line (leading whitespace + cuelang.org/go word boundary).
	// Skips comment-only lines starting with //.
	requireRe := regexp.MustCompile(`^\s*cuelang\.org/go\b`)

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if requireRe.MatchString(line) {
			t.Errorf("go.mod line %d still references cuelang.org/go: %q", lineNo, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan go.mod: %v", err)
	}
}
