package commands

// FR-3: --no-backup opt-in skip of rotateWorkflowBackups.
// FR-4: wrapper-vs-body hint when --hint-fixes is set.
// FR-5: BROWZER_LLM=1 implies --hint-fixes; --no-hint-fixes overrides.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalWorkflowFR uses a featureId that satisfies the CUE regex so that
// FR-3/4/5 tests don't fail on the featureId constraint before reaching
// the tested logic.
const minimalWorkflowFR = `{
  "schemaVersion": 2,
  "featureId": "feat-20260507-fr-test",
  "featureName": "fr-test",
  "featDir": "docs/browzer/feat-20260507-fr-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "updatedAt": "2026-05-07T00:00:00Z",
  "totalSteps": 0,
  "completedSteps": 0,
  "steps": []
}
`

// ---------------------------------------------------------------------------
// FR-3: --no-backup asserts .bak.* files are NOT created
// ---------------------------------------------------------------------------

// TestSaveStep_NoBackup_NoBakFilesCreated verifies that when --no-backup is
// set, no .bak.1 / .bak.2 files are created adjacent to workflow.json after
// a successful save-step invocation.
func TestSaveStep_NoBackup_NoBakFilesCreated(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "PRD",
		"--id", "feat-20260507-fr-test",
		"--stdin",
		"--quiet",
		"--no-backup",
	})
	cmd.SetIn(strings.NewReader(`{"title":"no-backup test","acceptanceCriteria":[]}`))
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save-step --no-backup failed: %v\nstderr=%s", err, errBuf.String())
	}

	// Assert that no .bak.* files were created.
	dir := filepath.Dir(wfPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Errorf("unexpected backup file created despite --no-backup: %s", e.Name())
		}
	}
}

// TestSaveStep_WithBackup_BakFileCreated verifies that WITHOUT --no-backup,
// a .bak.1 file IS created (the default behaviour). This guards the default
// path from accidentally being silenced.
func TestSaveStep_WithBackup_BakFileCreated(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "PRD",
		"--id", "feat-20260507-fr-test",
		"--stdin",
		"--quiet",
		// --no-backup NOT set — default rotation should run.
	})
	cmd.SetIn(strings.NewReader(`{"title":"with-backup test","acceptanceCriteria":[]}`))
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save-step (with backup) failed: %v\nstderr=%s", err, errBuf.String())
	}

	bak1 := wfPath + ".bak.1"
	if _, err := os.Stat(bak1); os.IsNotExist(err) {
		t.Errorf("expected .bak.1 file to be created by default rotation; not found at %s", bak1)
	}
}

// TestSaveStepBatch_NoBackup_NoBakFilesCreated verifies the same invariant for
// save-step-batch --no-backup.
func TestSaveStepBatch_NoBackup_NoBakFilesCreated(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	prdJSON := `{"title":"batch no-backup","acceptanceCriteria":[]}`
	prdFile := filepath.Join(root, "PRD_nb.json")
	if err := os.WriteFile(prdFile, []byte(prdJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step-batch",
		"--id", "feat-20260507-fr-test",
		"--from", "PRD=" + prdFile,
		"--quiet",
		"--no-backup",
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save-step-batch --no-backup failed: %v\nstderr=%s", err, errBuf.String())
	}

	dir := filepath.Dir(wfPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Errorf("unexpected backup file created despite --no-backup: %s", e.Name())
		}
	}
}

// ---------------------------------------------------------------------------
// FR-4: wrapper-vs-body hint on save-step / save-step-batch
// ---------------------------------------------------------------------------

// TestSaveStep_WrapperVsBodyHint_FiresWhenHintFixes verifies that when the
// caller submits a full step wrapper (containing `name`, `applicability`, and
// a body key) and --hint-fixes is set, the error message contains
// "Submit only the BODY".
func TestSaveStep_WrapperVsBodyHint_FiresWhenHintFixes(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	// Wrapper payload: name + applicability (wrapper-only) + tasksManifest (body key).
	wrapperPayload := `{
  "name": "TASKS_MANIFEST",
  "applicability": {"applicable": true},
  "tasksManifest": {
    "totalTasks": 1,
    "tasksOrder": ["TASK_01"],
    "tasks": []
  }
}`
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "TASKS_MANIFEST",
		"--id", "feat-20260507-fr-test",
		"--stdin",
		"--hint-fixes",
	})
	cmd.SetIn(strings.NewReader(wrapperPayload))
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for wrapper-vs-body submission; stderr=%s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "Submit only the BODY") {
		t.Errorf("expected stderr to contain %q; got:\n%s", "Submit only the BODY", errBuf.String())
	}
}

// TestSaveStep_WrapperVsBodyHint_SilentWithoutHintFixes verifies that the
// wrapper-vs-body check does NOT fire (no "Submit only the BODY" message)
// when --hint-fixes is not set. The payload still fails for other reasons
// (unknown fields) but the wrapper-hint-specific message must be absent.
func TestSaveStep_WrapperVsBodyHint_SilentWithoutHintFixes(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	wrapperPayload := `{
  "name": "TASKS_MANIFEST",
  "applicability": {"applicable": true},
  "tasksManifest": {
    "totalTasks": 0,
    "tasksOrder": [],
    "tasks": []
  }
}`
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "TASKS_MANIFEST",
		"--id", "feat-20260507-fr-test",
		"--stdin",
		// No --hint-fixes.
	})
	cmd.SetIn(strings.NewReader(wrapperPayload))
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	// We don't care about exit code here — the payload may succeed or fail
	// depending on the CUE schema. We only assert the specific hint is absent.
	_ = cmd.Execute()
	if strings.Contains(errBuf.String(), "Submit only the BODY") {
		t.Errorf("wrapper-vs-body hint must not fire without --hint-fixes; stderr:\n%s", errBuf.String())
	}
}

// TestDetectWrapperVsBodyHint_Unit exercises the pure detectWrapperVsBodyHint
// helper directly with a variety of inputs.
func TestDetectWrapperVsBodyHint_Unit(t *testing.T) {
	cases := []struct {
		name    string
		phase   string
		input   map[string]any
		wantHit bool
	}{
		{
			name:  "full wrapper → hit",
			phase: "TASKS_MANIFEST",
			input: map[string]any{
				"name":          "TASKS_MANIFEST",
				"applicability": map[string]any{"applicable": true},
				"tasksManifest": map[string]any{"totalTasks": 0},
			},
			wantHit: true,
		},
		{
			name:  "bare body → no hit",
			phase: "TASKS_MANIFEST",
			input: map[string]any{
				"totalTasks": 0,
				"tasksOrder": []any{},
			},
			wantHit: false,
		},
		{
			name:  "name mismatch → no hit",
			phase: "PRD",
			input: map[string]any{
				"name":          "TASKS_MANIFEST",
				"applicability": map[string]any{},
				"prd":           map[string]any{},
			},
			wantHit: false,
		},
		{
			name:  "wrapper-only fields but no body key → no hit",
			phase: "PRD",
			input: map[string]any{
				"name":          "PRD",
				"applicability": map[string]any{},
				"someOtherField": "x",
			},
			wantHit: false,
		},
		{
			name:  "CODE_REVIEW wrapper → hit",
			phase: "CODE_REVIEW",
			input: map[string]any{
				"name":       "CODE_REVIEW",
				"stepId":     "STEP_01_CODE_REVIEW",
				"codeReview": map[string]any{"findings": []any{}},
			},
			wantHit: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := detectWrapperVsBodyHint(tc.phase, tc.input)
			if tc.wantHit && hint == "" {
				t.Errorf("expected hint to fire for input=%v, got empty string", tc.input)
			}
			if !tc.wantHit && hint != "" {
				t.Errorf("expected no hint, got: %s", hint)
			}
			if tc.wantHit && !strings.Contains(hint, "Submit only the BODY") {
				t.Errorf("hint missing 'Submit only the BODY'; got: %s", hint)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FR-5: BROWZER_LLM=1 implies --hint-fixes; --no-hint-fixes overrides
// ---------------------------------------------------------------------------

// TestSaveStep_BrowzerLLM1_ImpliesHintFixes verifies that when BROWZER_LLM=1
// is set, a CUE validation failure prints worked-example hints WITHOUT the
// user passing --hint-fixes explicitly. We use a COMMIT payload with an
// invalid conventionalType enum value so CUE fires.
func TestSaveStep_BrowzerLLM1_ImpliesHintFixes(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_LLM", "1") // FR-5: implied hint-fixes.
	t.Chdir(root)

	commitPayload := `{"conventionalType": "bad-type", "scope": "cli", "subject": "test"}`
	fixturePath := filepath.Join(root, "commit-bad-fr5.json")
	if err := os.WriteFile(fixturePath, []byte(commitPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "COMMIT",
		"--id", "feat-20260507-fr-test",
		"--from", fixturePath,
		"--sync",
		// No --hint-fixes flag — BROWZER_LLM=1 should imply it.
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for bad enum; stderr=%s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "example:") {
		t.Errorf("BROWZER_LLM=1 should imply hint-fixes but 'example:' not found in stderr:\n%s", errBuf.String())
	}
}

// TestSaveStep_NoHintFixes_OverridesBrowzerLLM1 verifies that --no-hint-fixes
// suppresses hint output even when BROWZER_LLM=1 is set (FR-5 precedence).
func TestSaveStep_NoHintFixes_OverridesBrowzerLLM1(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_LLM", "1") // FR-5 env var set.
	t.Chdir(root)

	commitPayload := `{"conventionalType": "bad-type", "scope": "cli", "subject": "test"}`
	fixturePath := filepath.Join(root, "commit-bad-no-hint.json")
	if err := os.WriteFile(fixturePath, []byte(commitPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "COMMIT",
		"--id", "feat-20260507-fr-test",
		"--from", fixturePath,
		"--sync",
		"--no-hint-fixes", // must override BROWZER_LLM=1.
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for bad enum; stderr=%s", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "hint-fixes:") {
		t.Errorf("--no-hint-fixes must override BROWZER_LLM=1; hint-fixes: found in stderr:\n%s", errBuf.String())
	}
}

// TestResolveHintFixes_Unit exercises the resolveHintFixes helper directly.
func TestResolveHintFixes_Unit(t *testing.T) {
	cases := []struct {
		name        string
		hintFixes   bool
		noHintFixes bool
		browzerLLM  string
		want        bool
	}{
		{"explicit --hint-fixes", true, false, "", true},
		{"explicit --no-hint-fixes", false, true, "", false},
		{"LLM=1 implies hint-fixes", false, false, "1", true},
		{"LLM=true implies hint-fixes", false, false, "true", true},
		{"no-hint-fixes overrides LLM=1", false, true, "1", false},
		{"both flags: no-hint-fixes wins", true, true, "1", false},
		{"no flags, no LLM", false, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BROWZER_LLM", tc.browzerLLM)
			got := resolveHintFixes(tc.hintFixes, tc.noHintFixes)
			if got != tc.want {
				t.Errorf("resolveHintFixes(%v, %v) with BROWZER_LLM=%q = %v, want %v",
					tc.hintFixes, tc.noHintFixes, tc.browzerLLM, got, tc.want)
			}
		})
	}
}

// TestSaveStepBatch_NoHintFixes_OverridesBrowzerLLM1 verifies FR-5 precedence
// on the batch path.
func TestSaveStepBatch_NoHintFixes_OverridesBrowzerLLM1(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_LLM", "1")
	t.Chdir(root)

	badPRD := `{"bogusField": "cue-fails"}`
	prdFile := filepath.Join(root, "PRD_nohint.json")
	if err := os.WriteFile(prdFile, []byte(badPRD), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step-batch",
		"--id", "feat-20260507-fr-test",
		"--from", "PRD=" + prdFile,
		"--no-hint-fixes", // suppress even though BROWZER_LLM=1.
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit; stderr=%s", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "hint-fixes") {
		t.Errorf("--no-hint-fixes must suppress hints even with BROWZER_LLM=1; got:\n%s", errBuf.String())
	}
}

// ---------------------------------------------------------------------------
// Integration: --no-backup + verify workflow content is still written
// ---------------------------------------------------------------------------

// TestSaveStep_NoBackup_StillPersistsContent verifies that --no-backup does
// not prevent the actual write: the workflow.json should contain the new step.
func TestSaveStep_NoBackup_StillPersistsContent(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-fr-test", minimalWorkflowFR)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "PRD",
		"--id", "feat-20260507-fr-test",
		"--stdin",
		"--quiet",
		"--no-backup",
	})
	cmd.SetIn(strings.NewReader(`{"title":"persisted","acceptanceCriteria":[]}`))
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save-step --no-backup failed: %v\nstderr=%s", err, errBuf.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal workflow: %v", err)
	}
	steps, _ := doc["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step after --no-backup save, got %d", len(steps))
	}
	sm := steps[0].(map[string]any)
	if sm["name"] != "PRD" {
		t.Errorf("expected PRD step, got %v", sm["name"])
	}
}
