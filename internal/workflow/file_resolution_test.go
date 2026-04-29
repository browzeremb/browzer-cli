package workflow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveWorkflowPath_FlagOverridesAll verifies that an explicit --workflow
// flag path takes priority over the BROWZER_WORKFLOW env var and walk-up discovery.
// Covers T1-T-1: --workflow flag overrides env; env overrides walk-up.
func TestResolveWorkflowPath_FlagOverridesAll(t *testing.T) {
	dir := t.TempDir()
	flagPath := filepath.Join(dir, "from-flag", "workflow.json")
	if err := os.MkdirAll(filepath.Dir(flagPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flagPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// env var points somewhere else
	envPath := filepath.Join(dir, "from-env", "workflow.json")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWZER_WORKFLOW", envPath)

	var stderr bytes.Buffer
	got, err := ResolveWorkflowPath(flagPath, dir, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != flagPath {
		t.Errorf("expected flagPath %q, got %q", flagPath, got)
	}
}

// TestResolveWorkflowPath_EnvOverridesWalkUp verifies that the BROWZER_WORKFLOW
// env var takes priority over git-style walk-up discovery.
// Covers T1-T-1: env overrides walk-up.
func TestResolveWorkflowPath_EnvOverridesWalkUp(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "from-env", "workflow.json")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWZER_WORKFLOW", envPath)

	// Also plant a walk-up target — it should NOT win.
	featDir := filepath.Join(dir, "docs", "browzer", "feat-test")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	walkPath := filepath.Join(featDir, "workflow.json")
	if err := os.WriteFile(walkPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	// no flag path supplied
	got, err := ResolveWorkflowPath("", featDir, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != envPath {
		t.Errorf("expected envPath %q, got %q", envPath, got)
	}
}

// TestResolveWorkflowPath_WalkUpSucceedsFromFeatDir verifies that when no flag
// and no env, the walk-up strategy finds a workflow.json in a docs/browzer/feat-* dir.
// Covers T1-T-1: walk-up succeeds from a feat dir.
func TestResolveWorkflowPath_WalkUpSucceedsFromFeatDir(t *testing.T) {
	dir := t.TempDir()
	featDir := filepath.Join(dir, "docs", "browzer", "feat-something")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(featDir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BROWZER_WORKFLOW", "")

	var stderr bytes.Buffer
	got, err := ResolveWorkflowPath("", featDir, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wfPath {
		t.Errorf("expected wfPath %q, got %q", wfPath, got)
	}
}

// TestResolveWorkflowPath_WalkUpFailsOutsideFeatDir verifies that walk-up
// returns an error when the cwd is not inside any docs/browzer/feat-* dir.
// Covers T1-T-1: walk-up fails out of any feat dir.
func TestResolveWorkflowPath_WalkUpFailsOutsideFeatDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_WORKFLOW", "")

	var stderr bytes.Buffer
	_, err := ResolveWorkflowPath("", dir, &stderr)
	if err == nil {
		t.Error("expected error when not in a feat dir, got nil")
	}
}

// TestResolveWorkflowPath_AutoDiscoveredPathLoggedToStderr verifies that when
// the path is auto-discovered via walk-up (no flag, no env), a line of the
// form `resolved workflow: <path>` is emitted on the stderr writer.
// Covers T1-T-2: Auto-discovered path emits stderr line.
func TestResolveWorkflowPath_AutoDiscoveredPathLoggedToStderr(t *testing.T) {
	dir := t.TempDir()
	featDir := filepath.Join(dir, "docs", "browzer", "feat-log-test")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(featDir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWZER_WORKFLOW", "")

	var stderr bytes.Buffer
	got, err := ResolveWorkflowPath("", featDir, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stderrStr := stderr.String()
	expectedPrefix := "resolved workflow: "
	if !strings.Contains(stderrStr, expectedPrefix) {
		t.Errorf("expected stderr to contain %q, got %q", expectedPrefix, stderrStr)
	}
	if !strings.Contains(stderrStr, got) {
		t.Errorf("expected stderr to contain the resolved path %q, got %q", got, stderrStr)
	}
}

// TestResolveWorkflowPath_FlagDoesNotLogToStderr verifies that when a flag
// path is explicitly provided, no auto-discovery log line is emitted.
// Covers T1-T-2 (branch: flag path should NOT produce stderr noise).
func TestResolveWorkflowPath_FlagDoesNotLogToStderr(t *testing.T) {
	dir := t.TempDir()
	flagPath := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(flagPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWZER_WORKFLOW", "")

	var stderr bytes.Buffer
	_, err := ResolveWorkflowPath(flagPath, dir, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr.String(), "resolved workflow:") {
		t.Errorf("expected no 'resolved workflow:' log when flag is explicit, got %q", stderr.String())
	}
}

// TestResolveWorkflowPath_WalkUpFromNestedSubdir verifies AC-14 (FR-14):
// walk-up succeeds from a deeply nested CWD inside a feat dir. This tests
// the scenario where the caller's cwd is e.g. feat-test/drafts/v2/ — three
// levels below the workflow.json — which was previously only tested from the
// feat dir itself. Covers F-qa-2.
func TestResolveWorkflowPath_WalkUpFromNestedSubdir(t *testing.T) {
	// Set up: <tmp>/docs/browzer/feat-test/workflow.json
	//          <tmp>/docs/browzer/feat-test/drafts/v2/  (deepest cwd)
	dir := t.TempDir()
	featDir := filepath.Join(dir, "docs", "browzer", "feat-test")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(featDir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create the nested subdir that will be used as cwd.
	deepDir := filepath.Join(featDir, "drafts", "v2")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No flag, no env var — walk-up only.
	t.Setenv("BROWZER_WORKFLOW", "")

	var stderr bytes.Buffer
	got, err := ResolveWorkflowPath("", deepDir, &stderr)
	if err != nil {
		t.Fatalf("expected walk-up to succeed from nested subdir %q, got error: %v", deepDir, err)
	}
	if got != wfPath {
		t.Errorf("walk-up from nested subdir: expected %q, got %q", wfPath, got)
	}
}
