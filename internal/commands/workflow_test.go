package commands

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// minimalWorkflowJSON is a minimal valid workflow.json fixture.
const minimalWorkflowJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-test",
  "featureName": "Test Feature",
  "featDir": "docs/browzer/feat-test",
  "originalRequest": "do something",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}`

// buildWorkflowCommand constructs a fresh workflow cobra.Command with
// captured stdout and stderr buffers for testing.
//
// Note: when called from a *testing.T, prefer buildWorkflowCommandT — it
// also sets BROWZER_WORKFLOW_MODE=sync so the dispatch resolver forces the
// standalone path, avoiding stale-daemon flakiness (where a long-running
// daemon binary handles the test mutation with code that predates the test's
// own ApplyAndPersist). This signature is preserved for tests that already
// manage env state themselves.
func buildWorkflowCommand(stdout, stderr *bytes.Buffer) *cobra.Command {
	root := &cobra.Command{Use: "browzer"}
	registerWorkflow(root)
	if stdout != nil {
		root.SetOut(stdout)
	}
	if stderr != nil {
		root.SetErr(stderr)
	}
	return root
}

// buildWorkflowCommandT is the test-aware variant: forces the standalone
// dispatch path via BROWZER_WORKFLOW_MODE=sync so tests verify the in-process
// ApplyAndPersist behavior rather than whatever code a stale daemon binary
// happens to be running. Use this in any new test that touches
// append-step / set-status / complete-step / append-review-history / etc.
func buildWorkflowCommandT(t *testing.T, stdout, stderr *bytes.Buffer) *cobra.Command {
	t.Helper()
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	// TASK_02 / WF-SYNC-1 bypass: existing command tests use minimal
	// fixtures (`feat-test`, schemaVersion=1, missing required fields)
	// that pre-date the CUE schema cutover. They exercise CLI dispatch
	// + audit-line emission, NOT schema enforcement, so the env-var
	// bypass keeps them green without hand-rewriting every fixture.
	// New tests that DO want schema enforcement should clear the var
	// inside the test body via `t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")`.
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	return buildWorkflowCommand(stdout, stderr)
}

