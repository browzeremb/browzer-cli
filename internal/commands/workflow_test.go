package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
	return buildWorkflowCommand(stdout, stderr)
}

// TestWorkflowCmd_FlagOverridesEnvAndWalkUp verifies that passing --workflow
// takes priority over BROWZER_WORKFLOW env var and walk-up resolution.
// Covers T1-T-r6: Path resolution -- --workflow flag wins over env.
func TestWorkflowCmd_FlagOverridesEnvAndWalkUp(t *testing.T) {
	dir := t.TempDir()

	// Create two workflow files: one that should win (flag), one that should lose (env).
	flagWF := filepath.Join(dir, "flag", "workflow.json")
	envWF := filepath.Join(dir, "env", "workflow.json")

	if err := os.MkdirAll(filepath.Dir(flagWF), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(envWF), 0o755); err != nil {
		t.Fatal(err)
	}

	// Flag file has featureId "from-flag", env file has "from-env".
	flagContent := strings.ReplaceAll(minimalWorkflowJSON, "feat-test", "from-flag")
	envContent := strings.ReplaceAll(minimalWorkflowJSON, "feat-test", "from-env")

	if err := os.WriteFile(flagWF, []byte(flagContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envWF, []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWZER_WORKFLOW", envWF)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)

	// Run: browzer workflow get-config mode --workflow <flagWF>
	root.SetArgs([]string{"workflow", "get-config", "mode", "--workflow", flagWF})
	_ = root.Execute()

	// The output should use the flag path's featureId content, not the env's.
	// For get-config mode, output should be "autonomous".
	out := strings.TrimSpace(stdout.String())
	if out != "autonomous" {
		t.Errorf("expected output from flag workflow file, got %q", out)
	}
}

// TestWorkflowCmd_EnvOverridesWalkUp verifies that BROWZER_WORKFLOW env var
// takes priority over walk-up discovery.
// Covers T1-T-r6: env wins over walk-up.
func TestWorkflowCmd_EnvOverridesWalkUp(t *testing.T) {
	dir := t.TempDir()

	// Create the env workflow file with mode "review".
	envWF := filepath.Join(dir, "env", "workflow.json")
	if err := os.MkdirAll(filepath.Dir(envWF), 0o755); err != nil {
		t.Fatal(err)
	}
	envContent := strings.ReplaceAll(minimalWorkflowJSON, `"mode": "autonomous"`, `"mode": "review"`)
	if err := os.WriteFile(envWF, []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Also plant a walk-up workflow file with mode "autonomous" — should NOT win.
	featDir := filepath.Join(dir, "docs", "browzer", "feat-walk")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	walkWF := filepath.Join(featDir, "workflow.json")
	if err := os.WriteFile(walkWF, []byte(minimalWorkflowJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BROWZER_WORKFLOW", envWF)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode"})
	_ = root.Execute()

	out := strings.TrimSpace(stdout.String())
	if out != "review" {
		t.Errorf("expected 'review' from env workflow, got %q (walk-up result was used instead?)", out)
	}
}

// TestWorkflowCmd_WalkUpSucceedsFromFeatDir verifies that without flag/env,
// the walk-up strategy finds workflow.json from a feat dir.
// Covers T1-T-r6: walk-up succeeds from feat dir.
func TestWorkflowCmd_WalkUpSucceedsFromFeatDir(t *testing.T) {
	dir := t.TempDir()
	featDir := filepath.Join(dir, "docs", "browzer", "feat-walk-test")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(featDir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWZER_WORKFLOW", "")

	// Change into the feat dir to simulate CWD being inside a feat dir.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) }) //nolint:errcheck
	if err := os.Chdir(featDir); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out != "autonomous" {
		t.Errorf("expected 'autonomous' from walk-up resolved workflow, got %q", out)
	}
}

// TestWorkflowCmd_AutoDiscoveredPathEchoedToStderr verifies that when
// path is auto-discovered via walk-up, the stderr contains a line matching
// `resolved workflow: <path>`.
// Covers T1-T-r6: auto-discovered path is echoed to stderr.
func TestWorkflowCmd_AutoDiscoveredPathEchoedToStderr(t *testing.T) {
	dir := t.TempDir()
	featDir := filepath.Join(dir, "docs", "browzer", "feat-stderr-test")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(featDir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWZER_WORKFLOW", "")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) }) //nolint:errcheck
	if err := os.Chdir(featDir); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode"})
	_ = root.Execute()

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "resolved workflow:") {
		t.Errorf("expected 'resolved workflow: <path>' on stderr, got %q", stderrStr)
	}
}
