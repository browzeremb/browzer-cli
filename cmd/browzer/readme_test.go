package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/testutil"
)

// forbiddenVerbRE matches any reference to retired workflow-cluster verbs.
// The pattern covers:
//   - top-level legacy shorthands: browzer get-step, browzer save-step,
//     browzer save-step-batch (and any future *-step* variant)
//   - every browzer workflow <verb> form
//   - the "### Workflow state" heading used by the retired docs section
//
// Adding a new retired verb in a future cleanup PR automatically falls
// under the `browzer\s+workflow\s+\S+` arm — no list update required.
var forbiddenVerbRE = regexp.MustCompile(
	`browzer\s+(save-step|get-step|save-step-batch|workflow\s+\S+)` +
		`|### Workflow state`,
)

// TestREADMEFreeOfWorkflowVerbs pins that the README's command table
// no longer documents any of the retired workflow-cluster verbs.
// Pairs with the CHANGELOG regression to catch documentation drift.
func TestREADMEFreeOfWorkflowVerbs(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	readmePath := filepath.Join(repoRoot, "packages", "cli", "README.md")

	body, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}
	if loc := forbiddenVerbRE.FindString(string(body)); loc != "" {
		t.Errorf("README.md still contains retired surface matched by %s: %q", forbiddenVerbRE, loc)
	}
}
