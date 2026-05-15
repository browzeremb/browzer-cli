// Package testutil provides shared test helpers for the browzer-cli module.
// It must not be imported from production code.
package testutil

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// RepoRoot returns the absolute path to the repository root by running
// "git rev-parse --show-toplevel". The test is immediately failed if the
// command cannot be executed or exits non-zero.
//
// Using git rather than a runtime.Caller depth chain avoids the fragile
// filepath.Dir(…) ladder that breaks whenever a file is moved to a
// different directory depth.
func RepoRoot(t *testing.T) string {
	t.Helper()
	var out, errBuf bytes.Buffer
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("RepoRoot: git rev-parse --show-toplevel failed: %v\nstderr: %s", err, errBuf.String())
	}
	return strings.TrimSpace(out.String())
}
