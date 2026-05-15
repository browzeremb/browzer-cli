package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/testutil"
)

// versionRE validates the semver format and pins the MAJOR to 5.
// Any accidental downgrade to v4.x or promotion to v6.x will fail
// before the release tooling runs.
var versionRE = regexp.MustCompile(`^5\.\d+\.\d+$`)

// TestVERSIONFileEqualsExpected pins the on-disk VERSION file to a valid
// v5.x.x semver string. Bumping VERSION to 5.0.1, 5.1.0, etc. keeps
// the test green; an accidental v4 or v6 bump fails immediately.
func TestVERSIONFileEqualsExpected(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	versionPath := filepath.Join(repoRoot, "packages", "cli", "VERSION")

	body, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("read %s: %v", versionPath, err)
	}
	got := strings.TrimSpace(string(body))
	if !versionRE.MatchString(got) {
		t.Errorf("VERSION = %q; want semver matching %s", got, versionRE)
	}
}
