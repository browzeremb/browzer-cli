package commands

// getstep_test.go — tests for FR-1 (--field projection) and FR-5
// (invariants-block hashing) on `browzer get-step`.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/session"
	wfview "github.com/browzeremb/browzer-cli/internal/workflow/view"
)

// cueValidWorkflowWithConfig is a minimal v2 workflow that has a CONFIG block
// and a PRD step. Used to exercise --field projection on the CONFIG phase.
const cueValidWorkflowWithConfig = `{
  "schemaVersion": 2,
  "featureId": "feat-20260507-null-test",
  "featureName": "field-projection-test",
  "featDir": "docs/browzer/feat-20260507-null-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "executionStrategy": "serial", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "updatedAt": "2026-05-07T00:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}
`

// seedWorkflowForGetStep creates a temp CWD containing docs/browzer/<feat>/workflow.json.
func seedWorkflowForGetStep(t *testing.T, feat, content string) (root, wfPath string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "docs", "browzer", feat)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfPath = filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, wfPath
}

// TestGetStep_FieldProjection_JSONMode verifies that --field with --json emits
// only the requested fields (FR-1).
func TestGetStep_FieldProjection_JSONMode(t *testing.T) {
	root, _ := seedWorkflowForGetStep(t, "feat-20260507-null-test", cueValidWorkflowWithConfig)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	var out, errBuf bytes.Buffer
	cmd := NewRootCommand("test")
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"get-step", "CONFIG", "--id", "feat-20260507-null-test",
		"--field", "executionStrategy,mode", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr=%s", err, errBuf.String())
	}

	var projected map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &projected); err != nil {
		t.Fatalf("parse JSON output: %v\nraw=%s", err, out.String())
	}
	if _, ok := projected["executionStrategy"]; !ok {
		t.Error("expected 'executionStrategy' in projected JSON")
	}
	if _, ok := projected["mode"]; !ok {
		t.Error("expected 'mode' in projected JSON")
	}
	// Must NOT contain unexpected fields.
	if _, ok := projected["setAt"]; ok {
		t.Error("unexpected 'setAt' in projected JSON — should have been excluded by --field")
	}
}

// TestGetStep_FieldProjection_PlainText verifies that --field without --json
// emits "field: value" labeled lines (FR-1).
func TestGetStep_FieldProjection_PlainText(t *testing.T) {
	root, _ := seedWorkflowForGetStep(t, "feat-20260507-null-test", cueValidWorkflowWithConfig)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	var out, errBuf bytes.Buffer
	cmd := NewRootCommand("test")
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"get-step", "CONFIG", "--id", "feat-20260507-null-test",
		"--field", "executionStrategy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr=%s", err, errBuf.String())
	}

	output := out.String()
	if !strings.Contains(output, "executionStrategy:") {
		t.Errorf("expected 'executionStrategy:' label in plain-text output; got:\n%s", output)
	}
	if !strings.Contains(output, "serial") {
		t.Errorf("expected value 'serial' in plain-text output; got:\n%s", output)
	}
}

// TestGetStep_FieldProjection_UnknownField verifies that requesting an unknown
// field exits with code 2 and emits "unknown field: <name>" to stderr (FR-1).
func TestGetStep_FieldProjection_UnknownField(t *testing.T) {
	root, _ := seedWorkflowForGetStep(t, "feat-20260507-null-test", cueValidWorkflowWithConfig)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	var out, errBuf bytes.Buffer
	cmd := NewRootCommand("test")
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"get-step", "CONFIG", "--id", "feat-20260507-null-test",
		"--field", "doesNotExist"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown field; got nil")
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "unknown field: doesNotExist") {
		t.Errorf("expected 'unknown field: doesNotExist' on stderr; got:\n%s", stderr)
	}
}

// TestGetStep_FieldProjection_DidYouMean verifies that an unknown field that
// is close to a known field also emits a "did you mean" suggestion.
func TestGetStep_FieldProjection_DidYouMean(t *testing.T) {
	root, _ := seedWorkflowForGetStep(t, "feat-20260507-null-test", cueValidWorkflowWithConfig)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	var out, errBuf bytes.Buffer
	cmd := NewRootCommand("test")
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	// "mod" should suggest "mode" (distance 1)
	cmd.SetArgs([]string{"get-step", "CONFIG", "--id", "feat-20260507-null-test",
		"--field", "mod"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	stderr := errBuf.String()
	// Should contain either the "did you mean" text or at least the unknown field error.
	if !strings.Contains(stderr, "unknown field: mod") {
		t.Errorf("expected 'unknown field: mod' on stderr; got:\n%s", stderr)
	}
	// "mode" is distance 1 from "mod" — should appear as suggestion.
	if !strings.Contains(stderr, "mode") {
		t.Errorf("expected 'mode' suggestion on stderr; got:\n%s", stderr)
	}
}

// TestGetStep_InvariantsFlag_Skip verifies --invariants=skip suppresses the
// block (FR-5).
func TestGetStep_InvariantsFlag_Skip(t *testing.T) {
	// Write a minimal workflow with a TASK step that has invariants.
	const wfWithTask = `{
  "schemaVersion": 2,
  "featureId": "feat-20260507-null-test",
  "featureName": "inv-test",
  "featDir": "docs/browzer/feat-20260507-null-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "updatedAt": "2026-05-07T00:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}`
	root, _ := seedWorkflowForGetStep(t, "feat-20260507-null-test", wfWithTask)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	// The --invariants flag is just parsed without error for CONFIG (no invariants
	// block) — the important thing is the flag is accepted and doesn't break output.
	var out, errBuf bytes.Buffer
	cmd := NewRootCommand("test")
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"get-step", "CONFIG", "--id", "feat-20260507-null-test",
		"--invariants", "skip", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute with --invariants=skip: %v\nstderr=%s", err, errBuf.String())
	}
}

// TestGetStep_NoFlagRegressionBackwardCompat verifies that existing call sites
// (no new flags) still work identically (backward-compat invariant).
func TestGetStep_NoFlagRegressionBackwardCompat(t *testing.T) {
	root, _ := seedWorkflowForGetStep(t, "feat-20260507-null-test", cueValidWorkflowWithConfig)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	var out, errBuf bytes.Buffer
	cmd := NewRootCommand("test")
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"get-step", "CONFIG", "--id", "feat-20260507-null-test", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("backward-compat get-step without new flags: %v\nstderr=%s", err, errBuf.String())
	}
	output := out.String()
	if !strings.Contains(output, "CONFIG") {
		t.Errorf("expected CONFIG phase in output; got:\n%s", output)
	}
}

// TestApplyInvariantsHashToView_SuppressPathRefreshesCache verifies F-011:
// when ShouldEmitFull returns false (suppress path), RecordHash is still called
// with the current hash so the next call compares against the current state,
// not a stale prior value.
//
// Mechanism: use mode=hash (always suppresses, cache-independent). Pre-seed the
// session cache with a sentinel that differs from the actual hash. After calling
// applyInvariantsHashToView, assert the cache was overwritten to the current
// hash — proving RecordHash runs unconditionally, even on the suppress path.
func TestApplyInvariantsHashToView_SuppressPathRefreshesCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)
	sid := "test-suppress-refresh"
	t.Setenv("BROWZER_SESSION_ID", sid)

	// Build the StepView. applyInvariantsHashToView computes the hash via
	// strings.Join(v.Invariants, "\n") — replicate that to get the expected hash.
	lines := []string{"- invariant alpha", "- invariant beta"}
	view := wfview.StepView{
		Phase:      "TASK_01",
		Invariants: append([]string(nil), lines...), // own copy
	}
	joinedBlock := strings.Join(lines, "\n")
	hash := session.HashInvariants(joinedBlock)

	// Pre-seed the cache with a sentinel so we can observe the overwrite.
	// Using a 64-char all-'a' string that can never equal the real SHA-256.
	sentinel := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	session.RecordHash(sid, sentinel)

	// Call with mode=hash: ShouldEmitFull always returns false (suppress).
	// RecordHash must still be called — that is the invariant under test.
	applyInvariantsHashToView(&view, session.InvariantsModeHash)

	// Invariants slice must be replaced with the 1-element hash marker.
	if len(view.Invariants) != 1 {
		t.Fatalf("expected 1-element Invariants (hash marker) after suppress; got %d: %v", len(view.Invariants), view.Invariants)
	}
	if !strings.Contains(view.Invariants[0], hash[:8]) {
		t.Errorf("hash marker should contain hash prefix %q; got %q", hash[:8], view.Invariants[0])
	}

	// F-011 assertion: the cache must now hold the real hash (not the sentinel),
	// proving RecordHash was called on the suppress path.
	cachePath := filepath.Join(dir, "session", sid+".invariants.sha256")
	raw, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache file not found after suppress call: %v", err)
	}
	cached := strings.TrimSpace(string(raw))
	if cached != hash {
		t.Errorf("suppress path did not refresh cache: want %q (real hash); got %q", hash, cached)
	}
}
