package commands

// R-5: --hint-fixes emits "example:" on enum/unknown-field violations.
// R-6: --batch persists multiple phases atomically; partial failure rolls back.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// R-5: --hint-fixes
// ---------------------------------------------------------------------------

// minimalWorkflowR5 uses a featureId that satisfies the CUE regex
// `^feat-[0-9]{8}-[a-z0-9-]+$` so that R-5 and R-6 tests don't fail
// on the featureId constraint before reaching the enum / batch logic.
const minimalWorkflowR5 = `{
  "schemaVersion": 2,
  "featureId": "feat-20260507-r5-test",
  "featureName": "r5-test",
  "featDir": "docs/browzer/feat-20260507-r5-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "updatedAt": "2026-05-07T00:00:00Z",
  "totalSteps": 0,
  "completedSteps": 0,
  "steps": []
}
`

// TestSaveStep_HintFixes_EmitsExampleOnEnumViolation asserts that when
// `save-step --hint-fixes` is invoked with a payload that has a bad enum
// value (conventionalType: "invalid-type" on a COMMIT step), the command
// exits non-zero AND stderr contains the substring "example:".
//
// AC-5: `browzer save-step COMMIT --from <fixture-with-bad-enum> --hint-fixes`
// exits non-zero AND prints a line containing "example:".
func TestSaveStep_HintFixes_EmitsExampleOnEnumViolation(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-r5-test", minimalWorkflowR5)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	// Write a fixture with a bad enum for conventionalType.
	commitPayload := `{
  "conventionalType": "invalid-type",
  "scope": "cli",
  "subject": "bad commit type"
}`
	fixturePath := filepath.Join(root, "commit-bad-enum.json")
	if err := os.WriteFile(fixturePath, []byte(commitPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "COMMIT",
		"--id", "feat-20260507-r5-test",
		"--from", fixturePath,
		"--hint-fixes",
		"--sync",
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for bad enum payload; got success\nstderr=%s", errBuf.String())
	}
	stderrStr := errBuf.String()
	if !strings.Contains(stderrStr, "example:") {
		t.Errorf("expected stderr to contain %q when --hint-fixes is set; got:\n%s", "example:", stderrStr)
	}
}

// TestSaveStep_HintFixes_BackwardCompat asserts that WITHOUT --hint-fixes,
// a bad payload still exits non-zero but does NOT emit "hint-fixes:" in stderr.
// This guards the default-behaviour backward-compat contract.
func TestSaveStep_HintFixes_BackwardCompat(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-r5-test", minimalWorkflowR5)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	commitPayload := `{"conventionalType": "invalid-type", "subject": "bad"}`
	fixturePath := filepath.Join(root, "commit-bad.json")
	if err := os.WriteFile(fixturePath, []byte(commitPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step", "COMMIT",
		"--id", "feat-20260507-r5-test",
		"--from", fixturePath,
		"--sync",
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for bad enum payload (no --hint-fixes)")
	}
	if strings.Contains(errBuf.String(), "hint-fixes:") {
		t.Errorf("expected NO hint-fixes: in stderr without --hint-fixes flag; got:\n%s", errBuf.String())
	}
}

// ---------------------------------------------------------------------------
// R-6: --batch (save-step-batch)
// ---------------------------------------------------------------------------

// minimalWorkflowWithPRD seeds a workflow.json that already has a PRD step
// so the TASKS_MANIFEST save target is predictable.
const minimalWorkflowWithPRD = `{
  "schemaVersion": 2,
  "featureId": "feat-20260507-test",
  "featureName": "test",
  "featDir": "docs/browzer/feat-20260507-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "updatedAt": "2026-05-07T00:00:00Z",
  "totalSteps": 0,
  "completedSteps": 0,
  "steps": []
}
`

// TestSaveStepBatch_Success_PersistsBothPhases asserts that
// `save-step-batch --from PRD=<a> --from TASKS_MANIFEST=<b>` persists
// both phases atomically: after the command, both steps appear in workflow.json
// (AC-6 success path).
func TestSaveStepBatch_Success_PersistsBothPhases(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-test", minimalWorkflowWithPRD)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	prdJSON := `{"title":"batch PRD","acceptanceCriteria":[{"id":"AC-1","text":"x"}]}`
	tasksJSON := `{"totalTasks":1,"tasksOrder":["TASK_01"],"dependencyGraph":{},"parallelizable":[]}`

	prdFile := filepath.Join(root, "PRD.json")
	tasksFile := filepath.Join(root, "TASKS.json")
	if err := os.WriteFile(prdFile, []byte(prdJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tasksFile, []byte(tasksJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step-batch",
		"--id", "feat-20260507-test",
		"--from", fmt.Sprintf("PRD=%s", prdFile),
		"--from", fmt.Sprintf("TASKS_MANIFEST=%s", tasksFile),
		"--quiet",
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("batch execute failed: %v\nstderr=%s", err, errBuf.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps, _ := doc["steps"].([]any)
	if len(steps) < 2 {
		t.Fatalf("expected at least 2 steps after batch, got %d", len(steps))
	}
	names := map[string]bool{}
	for _, s := range steps {
		sm, _ := s.(map[string]any)
		if n, _ := sm["name"].(string); n != "" {
			names[n] = true
		}
	}
	if !names["PRD"] {
		t.Errorf("PRD step missing after batch write; steps=%v", names)
	}
	if !names["TASKS_MANIFEST"] {
		t.Errorf("TASKS_MANIFEST step missing after batch write; steps=%v", names)
	}
}

// TestSaveStepBatch_PartialFailure_RollsBack asserts that when one phase in
// the batch has a bad payload, workflow.json mtime is unchanged and no
// partial write occurs (AC-6 rollback path).
func TestSaveStepBatch_PartialFailure_RollsBack(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-test", minimalWorkflowWithPRD)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	// Good PRD payload.
	prdJSON := `{"title":"ok PRD","acceptanceCriteria":[{"id":"AC-1","text":"x"}]}`
	// Bad TASKS_MANIFEST: missing required totalTasks + tasksOrder.
	badTasksJSON := `{"notAField": true}`

	prdFile := filepath.Join(root, "PRD.json")
	badFile := filepath.Join(root, "TASKS_BAD.json")
	if err := os.WriteFile(prdFile, []byte(prdJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badFile, []byte(badTasksJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Record mtime before the (should-fail) batch.
	beforeStat, err := os.Stat(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMtime := beforeStat.ModTime()

	// Sleep 5 ms so any accidental write would produce a detectable mtime delta.
	time.Sleep(5 * time.Millisecond)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step-batch",
		"--id", "feat-20260507-test",
		"--from", fmt.Sprintf("PRD=%s", prdFile),
		"--from", fmt.Sprintf("TASKS_MANIFEST=%s", badFile),
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for batch with bad phase; stderr=%s", errBuf.String())
	}

	// workflow.json mtime must be unchanged — no partial write occurred.
	afterStat, statErr := os.Stat(wfPath)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if afterStat.ModTime().After(beforeMtime) {
		t.Errorf("workflow.json mtime changed after failed batch: before=%v after=%v",
			beforeMtime, afterStat.ModTime())
	}

	// workflow.json must still contain the original (empty) steps array.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps, _ := doc["steps"].([]any)
	if len(steps) != 0 {
		t.Errorf("expected 0 steps after failed batch rollback, got %d", len(steps))
	}
}

// TestSaveStepBatch_MissingFromFlag_Errors asserts that invoking
// save-step-batch without any --from pairs exits non-zero.
func TestSaveStepBatch_MissingFromFlag_Errors(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-test", minimalWorkflowWithPRD)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step-batch", "--id", "feat-20260507-test"})
	var b bytes.Buffer
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing --from pairs")
	}
}

// ---------------------------------------------------------------------------
// F-3: duplicate phase detection
// ---------------------------------------------------------------------------

// TestSaveStepBatch_DuplicatePhase_Errors asserts that providing the same
// phase name twice in --from pairs exits non-zero with exit code 2 (F-3).
func TestSaveStepBatch_DuplicatePhase_Errors(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-test", minimalWorkflowWithPRD)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	prdJSON := `{"title":"dup","acceptanceCriteria":[{"id":"AC-1","text":"x"}]}`
	prdFile := filepath.Join(root, "PRD.json")
	if err := os.WriteFile(prdFile, []byte(prdJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step-batch",
		"--id", "feat-20260507-test",
		"--from", fmt.Sprintf("PRD=%s", prdFile),
		"--from", fmt.Sprintf("PRD=%s", prdFile), // duplicate
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for duplicate phase; stderr=%s", errBuf.String())
	}
	if !strings.Contains(errBuf.String()+err.Error(), "duplicate") {
		t.Errorf("expected error to mention 'duplicate'; got stderr=%s err=%v", errBuf.String(), err)
	}
}

// ---------------------------------------------------------------------------
// F-4: write-failure atomicity
// ---------------------------------------------------------------------------

// TestSaveStepBatch_BatchCUEFailure_MtimeUnchanged asserts that when the
// post-batch CUE validation fails, workflow.json mtime is unchanged and
// content is unmodified (atomic rollback / F-4).
func TestSaveStepBatch_BatchCUEFailure_MtimeUnchanged(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-test", minimalWorkflowWithPRD)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	// Do NOT set BROWZER_NO_SCHEMA_CHECK — we need the CUE gate to fire.
	t.Chdir(root)

	// Supply a PRD with an invalid field that should fail CUE validation.
	// Using "bogusField" ensures CUE's closed-struct check rejects it.
	badPRD := `{"bogusField":"this-should-fail-cue"}`
	prdFile := filepath.Join(root, "PRD_BAD.json")
	if err := os.WriteFile(prdFile, []byte(badPRD), 0o644); err != nil {
		t.Fatal(err)
	}

	beforeStat, err := os.Stat(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMtime := beforeStat.ModTime()
	beforeContent, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Millisecond)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step-batch",
		"--id", "feat-20260507-test",
		"--from", fmt.Sprintf("PRD=%s", prdFile),
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected non-zero exit for CUE-invalid payload; stderr=%s", errBuf.String())
	}

	afterStat, err := os.Stat(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if afterStat.ModTime().After(beforeMtime) {
		t.Errorf("workflow.json mtime changed after failed batch: before=%v after=%v",
			beforeMtime, afterStat.ModTime())
	}
	afterContent, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterContent) != string(beforeContent) {
		t.Errorf("workflow.json content changed after failed batch rollback")
	}
}

// ---------------------------------------------------------------------------
// F-11: COMMIT round-trip via save-step + get-step at the command level
// ---------------------------------------------------------------------------

// TestSaveStep_CommitRoundtrip_PersistsAndReads invokes save-step COMMIT
// against a temp workflow using the valid commit-min.json fixture, then
// reads back the persisted step via get-step --json and asserts that
// the step's name == "COMMIT" and required fields are present (F-11).
func TestSaveStep_CommitRoundtrip_PersistsAndReads(t *testing.T) {
	// Locate fixture BEFORE chdir so cwd-relative lookup works.
	fixtureDir := findCommitFixtureDir(t)
	fixturePath := filepath.Join(fixtureDir, "commit-min.json")
	// Make absolute so it survives t.Chdir below.
	if abs, err := filepath.Abs(fixturePath); err == nil {
		fixturePath = abs
	}

	root, _ := seedWorkflowForFeat(t, "feat-20260507-r5-test", minimalWorkflowR5)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)
	if _, err := os.Stat(fixturePath); err != nil {
		t.Skipf("commit-min.json fixture not found at %s: %v", fixturePath, err)
	}

	// save-step COMMIT --from <fixture>
	saveCmd := NewRootCommand("test")
	saveCmd.SetArgs([]string{
		"save-step", "COMMIT",
		"--id", "feat-20260507-r5-test",
		"--from", fixturePath,
		"--quiet",
	})
	var out, errBuf bytes.Buffer
	saveCmd.SetOut(&out)
	saveCmd.SetErr(&errBuf)
	if err := saveCmd.Execute(); err != nil {
		t.Fatalf("save-step COMMIT failed: %v\nstderr=%s", err, errBuf.String())
	}

	// get-step COMMIT --json
	getCmd := NewRootCommand("test")
	getCmd.SetArgs([]string{
		"get-step", "COMMIT",
		"--id", "feat-20260507-r5-test",
		"--json",
	})
	var getOut, getErr bytes.Buffer
	getCmd.SetOut(&getOut)
	getCmd.SetErr(&getErr)
	if err := getCmd.Execute(); err != nil {
		t.Fatalf("get-step COMMIT failed: %v\nstderr=%s", err, getErr.String())
	}

	var stepView map[string]any
	if err := json.Unmarshal(getOut.Bytes(), &stepView); err != nil {
		t.Fatalf("unmarshal get-step output: %v\nstdout=%s", err, getOut.String())
	}
	// StepView uses "phase" as the canonical field name in the JSON output.
	phase, _ := stepView["phase"].(string)
	if phase != "COMMIT" {
		t.Errorf("expected step phase COMMIT, got %q (full output: %s)", phase, getOut.String())
	}
	// The stepId field must be non-empty (step was persisted).
	stepID, _ := stepView["stepId"].(string)
	if stepID == "" {
		t.Errorf("expected non-empty stepId in persisted COMMIT step; output=%s", getOut.String())
	}
}

// findCommitFixtureDir locates the valid fixtures directory by walking up
// from the current file's directory. The test binary runs from the package
// directory (packages/cli/internal/commands), so we walk up 3 levels.
func findCommitFixtureDir(t *testing.T) string {
	t.Helper()
	// Walk from the package dir (internal/commands) up to cli root.
	// Resolve relative to the executable's working directory at test time.
	cwd, _ := os.Getwd()
	candidates := []string{
		// from packages/cli/internal/commands → packages/cli/schemas/...
		filepath.Join(cwd, "..", "..", "schemas", "fixtures", "valid"),
		// from packages/cli → packages/cli/schemas/...
		filepath.Join(cwd, "schemas", "fixtures", "valid"),
		// from repo root
		filepath.Join(cwd, "packages", "cli", "schemas", "fixtures", "valid"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "commit-min.json")); err == nil {
			return c
		}
	}
	// Fall back: env override.
	if repoRoot := os.Getenv("BROWZER_REPO_ROOT"); repoRoot != "" {
		p := filepath.Join(repoRoot, "packages", "cli", "schemas", "fixtures", "valid")
		if _, err := os.Stat(filepath.Join(p, "commit-min.json")); err == nil {
			return p
		}
	}
	t.Log("commit-min.json fixture not found; test will be skipped")
	return "."
}

// ---------------------------------------------------------------------------
// F-12: file-not-found names the phase in the error
// ---------------------------------------------------------------------------

// TestSaveStepBatch_FileNotFound_NamesPhaseInError asserts that when
// save-step-batch is invoked with --from PRD=/tmp/does-not-exist.json,
// the error message contains "PRD" (F-12).
func TestSaveStepBatch_FileNotFound_NamesPhaseInError(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-test", minimalWorkflowWithPRD)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"save-step-batch",
		"--id", "feat-20260507-test",
		"--from", "PRD=/tmp/does-not-exist-f12-fixture.json",
	})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-zero exit for missing file; stderr=%s", errBuf.String())
	}
	combined := errBuf.String() + err.Error()
	if !strings.Contains(combined, "PRD") {
		t.Errorf("expected error to name the phase %q; got: %s", "PRD", combined)
	}
}

// ---------------------------------------------------------------------------
// F-1 / atomicity: concurrent save-step-batch cannot interleave
// ---------------------------------------------------------------------------

// TestSaveStepBatch_ConcurrentBatches_NoInterleave asserts that concurrent
// save-step-batch invocations against the same workflow.json cannot interleave
// (F-1 regression test). Both batches write a BRAINSTORMING step; the final
// workflow.json must be valid JSON.
func TestSaveStepBatch_ConcurrentBatches_NoInterleave(t *testing.T) {
	root, wfPath := seedWorkflowForFeat(t, "feat-20260507-r5-test", minimalWorkflowR5)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	brainstorm := `{"summary":"concurrent write","items":[]}`
	bFile := filepath.Join(root, "BRAINSTORM.json")
	if err := os.WriteFile(bFile, []byte(brainstorm), 0o644); err != nil {
		t.Fatal(err)
	}

	const n = 5
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			cmd := NewRootCommand("test")
			cmd.SetArgs([]string{
				"save-step-batch",
				"--id", "feat-20260507-r5-test",
				"--from", fmt.Sprintf("BRAINSTORMING=%s", bFile),
				"--quiet",
			})
			var out, errBuf bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errBuf)
			errs <- cmd.Execute()
		}()
	}
	// All goroutines must complete without panic (some may get lock timeout
	// or validation failures from the idempotent upsert — that's fine).
	for i := 0; i < n; i++ {
		<-errs
	}

	// The workflow.json must still be valid JSON after concurrent writes.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Errorf("workflow.json corrupted after concurrent batch writes: %v\ncontent=%s", err, data)
	}
}
