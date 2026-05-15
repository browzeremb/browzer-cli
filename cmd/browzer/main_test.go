package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/testutil"
)

// TestBuildGraphLacksWorkflowPackage asserts that the import path
// github.com/browzeremb/browzer-cli/internal/workflow is absent from
// the full build graph of this CLI binary.
func TestBuildGraphLacksWorkflowPackage(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	cliRoot := filepath.Join(repoRoot, "packages", "cli")

	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = cliRoot

	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		t.Fatalf("go list ./... failed: %v\nstderr: %s", err, errBuf.String())
	}

	const banned = "github.com/browzeremb/browzer-cli/internal/workflow"
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(line) == banned {
			t.Fatalf("build graph still contains package %q — directory deletion was incomplete", banned)
		}
	}
}
