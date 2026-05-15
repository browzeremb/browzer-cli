package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/testutil"
)

// TestCHANGELOGHasV500RemovedSection pins that CHANGELOG.md gained a
// v5.0.0 section with a "### Removed" block enumerating the legacy
// workflow.json cluster. Any future PR that forgets to maintain the
// changelog entry around a major bump fails the test before release.
func TestCHANGELOGHasV500RemovedSection(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	changelogPath := filepath.Join(repoRoot, "packages", "cli", "CHANGELOG.md")

	body, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatalf("read %s: %v", changelogPath, err)
	}
	headerRE := regexp.MustCompile(`(?m)^## \[5\.0\.0\]`)
	if !headerRE.Match(body) {
		t.Errorf("CHANGELOG.md missing `## [5.0.0]` header")
	}
	removedRE := regexp.MustCompile(`(?m)^### Removed`)
	if !removedRE.Match(body) {
		t.Errorf("CHANGELOG.md missing `### Removed` block")
	}
}
