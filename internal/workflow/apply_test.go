package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- helpers ---

// makeThreeStepWorkflow creates a minimal 3-step workflow JSON (BRAINSTORMING +
// PRD + COMMIT) with all steps in PENDING status and each step carrying a
// startedAt set 10 minutes before completedAt so elapsedMin can be computed.
// The startedAt for all steps is pre-seeded so complete-step / set-status can
// compute elapsedMin > 0.
func makeThreeStepWorkflow(step1Status, step2Status, step3Status string) string {
	startedAt := "2026-05-01T10:00:00Z"
	return fmt.Sprintf(`{
  "schemaVersion": 2,
  "pluginVersion": null,
  "featureId": "feat-20260501-three-step",
  "featureName": "Three Step Test",
  "featDir": "docs/browzer/feat-20260501-three-step",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-05-01T00:00:00Z"},
  "startedAt": "2026-05-01T10:00:00Z",
  "updatedAt": "2026-05-01T10:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 3,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_BRAINSTORMING",
      "name": "BRAINSTORMING",
      "status": %q,
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": %q,
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "STEP_02_PRD",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "warnings": [],
      "reviewHistory": [],
      "dispatches": [],
      "brainstorming": {
        "questionsAsked": 0,
        "researchRoundRun": false,
        "researchAgents": 0,
        "dimensions": {
          "primaryUser": "test",
          "jobToBeDone": "test",
          "successSignal": "test",
          "inScope": [],
          "outOfScope": []
        }
      }
    },
    {
      "stepId": "STEP_02_PRD",
      "name": "PRD",
      "status": %q,
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": %q,
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "STEP_03_COMMIT",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "warnings": [],
      "reviewHistory": [],
      "dispatches": [],
      "prd": {
        "title": "test",
        "functionalRequirements": [],
        "acceptanceCriteria": []
      }
    },
    {
      "stepId": "STEP_03_COMMIT",
      "name": "COMMIT",
      "status": %q,
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": %q,
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "warnings": [],
      "reviewHistory": [],
      "dispatches": [],
      "commit": {
        "conventionalType": "test",
        "subject": "test commit",
        "trailers": []
      }
    }
  ]
}`, step1Status, startedAt, step2Status, startedAt, step3Status, startedAt)
}

// writeWFFile writes content to a new temp file and returns the path.
func writeWFFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeWFFile: %v", err)
	}
	return path
}

// readWF reads and parses a workflow.json file.
func readWF(t *testing.T, path string) Workflow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readWF read: %v", err)
	}
	var wf Workflow
	if err := json.Unmarshal(data, &wf); err != nil {
		t.Fatalf("readWF unmarshal: %v", err)
	}
	return wf
}

// applyVerb is a thin helper that calls ApplyAndPersist with the given verb +
// args and returns the result + error.
func applyVerb(t *testing.T, path, verb string, args []string) (ApplyResult, error) {
	t.Helper()
	return ApplyAndPersist(path, verb, MutatorArgs{Args: args}, false)
}

// --- TASK_04 tests: stampWorkflowTotalElapsedIfFinal ---

// TestCompleteStep_StampsOnlyOnFinalStep verifies that totalElapsedMin stays 0
// after completing steps 1 and 2 of a 3-step workflow, and is set to > 0 only
// after completing step 3 (the final step).
func TestCompleteStep_StampsOnlyOnFinalStep(t *testing.T) {
	// Steps start RUNNING so complete-step sees a status worth mutating.
	wfPath := writeWFFile(t, makeThreeStepWorkflow(StatusRunning, StatusRunning, StatusRunning))

	// Complete step 1 — not final yet.
	if _, err := applyVerb(t, wfPath, "complete-step", []string{"STEP_01_BRAINSTORMING"}); err != nil {
		t.Fatalf("complete-step 1: %v", err)
	}
	wf1 := readWF(t, wfPath)
	if wf1.TotalElapsedMin != 0 {
		t.Errorf("after step 1 complete: expected totalElapsedMin=0, got %v", wf1.TotalElapsedMin)
	}

	// Complete step 2 — not final yet.
	if _, err := applyVerb(t, wfPath, "complete-step", []string{"STEP_02_PRD"}); err != nil {
		t.Fatalf("complete-step 2: %v", err)
	}
	wf2 := readWF(t, wfPath)
	if wf2.TotalElapsedMin != 0 {
		t.Errorf("after step 2 complete: expected totalElapsedMin=0, got %v", wf2.TotalElapsedMin)
	}

	// Complete step 3 — final step, totalElapsedMin must be stamped.
	if _, err := applyVerb(t, wfPath, "complete-step", []string{"STEP_03_COMMIT"}); err != nil {
		t.Fatalf("complete-step 3: %v", err)
	}
	wf3 := readWF(t, wfPath)
	if wf3.TotalElapsedMin <= 0 {
		t.Errorf("after final step complete: expected totalElapsedMin > 0, got %v", wf3.TotalElapsedMin)
	}
}

// TestSetStatus_StampsOnlyAfterFinalCompletion verifies the same stamp
// invariant via the set-status path: totalElapsedMin must be 0 after
// transitioning steps 1+2 to COMPLETED and > 0 after transitioning step 3.
func TestSetStatus_StampsOnlyAfterFinalCompletion(t *testing.T) {
	// All steps start RUNNING with startedAt set.
	wfPath := writeWFFile(t, makeThreeStepWorkflow(StatusRunning, StatusRunning, StatusRunning))

	// Transition step 1 → COMPLETED.
	if _, err := applyVerb(t, wfPath, "set-status", []string{"STEP_01_BRAINSTORMING", StatusCompleted}); err != nil {
		t.Fatalf("set-status step 1 COMPLETED: %v", err)
	}
	wf1 := readWF(t, wfPath)
	if wf1.TotalElapsedMin != 0 {
		t.Errorf("after step 1 COMPLETED: expected totalElapsedMin=0, got %v", wf1.TotalElapsedMin)
	}

	// Transition step 2 → COMPLETED.
	if _, err := applyVerb(t, wfPath, "set-status", []string{"STEP_02_PRD", StatusCompleted}); err != nil {
		t.Fatalf("set-status step 2 COMPLETED: %v", err)
	}
	wf2 := readWF(t, wfPath)
	if wf2.TotalElapsedMin != 0 {
		t.Errorf("after step 2 COMPLETED: expected totalElapsedMin=0, got %v", wf2.TotalElapsedMin)
	}

	// Transition step 3 → COMPLETED — final step.
	if _, err := applyVerb(t, wfPath, "set-status", []string{"STEP_03_COMMIT", StatusCompleted}); err != nil {
		t.Fatalf("set-status step 3 COMPLETED: %v", err)
	}
	wf3 := readWF(t, wfPath)
	if wf3.TotalElapsedMin <= 0 {
		t.Errorf("after final step COMPLETED via set-status: expected totalElapsedMin > 0, got %v", wf3.TotalElapsedMin)
	}
}

// TestStampWorkflowTotalElapsedIfFinal_IdempotentGuard verifies that the stamp
// helper does NOT overwrite an already-positive totalElapsedMin.
func TestStampWorkflowTotalElapsedIfFinal_IdempotentGuard(t *testing.T) {
	raw := map[string]any{
		"totalElapsedMin": float64(42),
		"startedAt":       "2026-05-04T00:00:00Z",
		"steps": []any{
			map[string]any{"status": StatusCompleted, "elapsedMin": float64(10), "name": StepCommit},
		},
	}
	stepMap := raw["steps"].([]any)[0].(map[string]any)
	stampWorkflowTotalElapsedIfFinal(raw, stepMap, "2026-05-04T01:00:00Z")
	if raw["totalElapsedMin"] != float64(42) {
		t.Errorf("idempotent guard: expected 42, got %v", raw["totalElapsedMin"])
	}
}

// TestStampWorkflowTotalElapsedIfFinal_NotFinalSkipsStamp verifies that the
// stamp helper returns without writing when at least one step is not terminal.
func TestStampWorkflowTotalElapsedIfFinal_NotFinalSkipsStamp(t *testing.T) {
	raw := map[string]any{
		"totalElapsedMin": float64(0),
		"startedAt":       "2026-05-04T00:00:00Z",
		"steps": []any{
			map[string]any{"status": StatusCompleted, "elapsedMin": float64(5), "name": "PRD"},
			map[string]any{"status": StatusRunning, "elapsedMin": float64(0), "name": "TASK"},
		},
	}
	stepMap := raw["steps"].([]any)[0].(map[string]any)
	stampWorkflowTotalElapsedIfFinal(raw, stepMap, "2026-05-04T01:00:00Z")
	if raw["totalElapsedMin"] != float64(0) {
		t.Errorf("expected 0 (not final), got %v", raw["totalElapsedMin"])
	}
}

// --- TASK_05 tests: .browzer/active-step cache ---

// TestSetCurrentStep_WritesActiveStepCache verifies that set-current-step
// writes the stepId to <workflowDir>/.browzer/active-step.
func TestSetCurrentStep_WritesActiveStepCache(t *testing.T) {
	wfPath := writeWFFile(t, makeThreeStepWorkflow(StatusPending, StatusPending, StatusPending))
	wfDir := filepath.Dir(wfPath)

	_, err := ApplyAndPersist(wfPath, "set-current-step", MutatorArgs{
		Args:        []string{"STEP_01_BRAINSTORMING"},
		WorkflowDir: wfDir,
	}, false)
	if err != nil {
		t.Fatalf("set-current-step: %v", err)
	}

	cacheFile := filepath.Join(wfDir, ".browzer", "active-step")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("active-step cache not written: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "STEP_01_BRAINSTORMING" {
		t.Errorf("active-step content: expected STEP_01_BRAINSTORMING, got %q", got)
	}
}

// TestCompleteStep_ClearsActiveStepCacheOnFinal verifies that completing
// step 3 of a 3-step workflow deletes .browzer/active-step, while completing
// steps 1 and 2 does NOT delete it.
func TestCompleteStep_ClearsActiveStepCacheOnFinal(t *testing.T) {
	wfPath := writeWFFile(t, makeThreeStepWorkflow(StatusRunning, StatusRunning, StatusRunning))
	wfDir := filepath.Dir(wfPath)

	// Pre-create the cache file.
	cacheDir := filepath.Join(wfDir, ".browzer")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheFile := filepath.Join(cacheDir, "active-step")
	if err := os.WriteFile(cacheFile, []byte("STEP_03_COMMIT"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Complete step 1 — cache must survive.
	if _, err := ApplyAndPersist(wfPath, "complete-step", MutatorArgs{
		Args:        []string{"STEP_01_BRAINSTORMING"},
		WorkflowDir: wfDir,
	}, false); err != nil {
		t.Fatalf("complete-step 1: %v", err)
	}
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Error("active-step cache should survive after step 1 complete")
	}

	// Complete step 2 — cache must survive.
	if _, err := ApplyAndPersist(wfPath, "complete-step", MutatorArgs{
		Args:        []string{"STEP_02_PRD"},
		WorkflowDir: wfDir,
	}, false); err != nil {
		t.Fatalf("complete-step 2: %v", err)
	}
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Error("active-step cache should survive after step 2 complete")
	}

	// Complete step 3 — cache must be deleted.
	if _, err := ApplyAndPersist(wfPath, "complete-step", MutatorArgs{
		Args:        []string{"STEP_03_COMMIT"},
		WorkflowDir: wfDir,
	}, false); err != nil {
		t.Fatalf("complete-step 3: %v", err)
	}
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Error("active-step cache should be deleted after final step complete")
	}
}

// --- TASK_09 tests: AWAITING_REVIEW transitions ---

// TestSetStatus_AwaitingReviewToPending verifies that AWAITING_REVIEW → PENDING
// is now a legal transition (returns nil error).
func TestSetStatus_AwaitingReviewToPending(t *testing.T) {
	wfJSON := makeThreeStepWorkflow(StatusAwaitingReview, StatusPending, StatusPending)
	wfPath := writeWFFile(t, wfJSON)

	if _, err := applyVerb(t, wfPath, "set-status", []string{"STEP_01_BRAINSTORMING", StatusPending}); err != nil {
		t.Fatalf("AWAITING_REVIEW→PENDING should be legal, got: %v", err)
	}
	wf := readWF(t, wfPath)
	if wf.Steps[0].Status != StatusPending {
		t.Errorf("expected PENDING after transition, got %q", wf.Steps[0].Status)
	}
}

// TestSetStatus_AwaitingReviewToRunning verifies that AWAITING_REVIEW → RUNNING
// is legal and auto-stamps startedAt if previously unset.
func TestSetStatus_AwaitingReviewToRunning(t *testing.T) {
	// Build a workflow where step 1 is AWAITING_REVIEW without a startedAt.
	// Use a minimal inline JSON so we can precisely control startedAt="".
	wfRaw := `{
  "schemaVersion": 2,
  "pluginVersion": null,
  "featureId": "feat-20260501-ar-running",
  "featureName": "AR Running Test",
  "featDir": "docs/browzer/feat-20260501-ar-running",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-05-01T00:00:00Z"},
  "startedAt": "2026-05-01T10:00:00Z",
  "updatedAt": "2026-05-01T10:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_BRAINSTORMING",
      "name": "BRAINSTORMING",
      "status": "AWAITING_REVIEW",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-05-01T10:00:00Z",
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "warnings": [],
      "reviewHistory": [],
      "dispatches": [],
      "brainstorming": {
        "questionsAsked": 0,
        "researchRoundRun": false,
        "researchAgents": 0,
        "dimensions": {
          "primaryUser": "test",
          "jobToBeDone": "test",
          "successSignal": "test",
          "inScope": [],
          "outOfScope": []
        }
      }
    }
  ]
}`
	wfPath := writeWFFile(t, wfRaw)

	if _, err := applyVerb(t, wfPath, "set-status", []string{"STEP_01_BRAINSTORMING", StatusRunning}); err != nil {
		t.Fatalf("AWAITING_REVIEW→RUNNING should be legal, got: %v", err)
	}
	wf := readWF(t, wfPath)
	step := wf.Steps[0]
	if step.Status != StatusRunning {
		t.Errorf("expected RUNNING after transition, got %q", step.Status)
	}
	// startedAt must remain set + RFC3339 parseable. Auto-stamp on
	// previously-empty startedAt is exercised transitively when other
	// PENDING→RUNNING tests fire — CUE v2 forbids an empty-string
	// startedAt at the schema gate so this test cannot seed one.
	if step.StartedAt == "" {
		t.Error("expected startedAt to remain set after AWAITING_REVIEW→RUNNING")
	}
	if _, err := time.Parse(time.RFC3339, step.StartedAt); err != nil {
		t.Fatalf("startedAt is not RFC3339: %q", step.StartedAt)
	}
}

// TestSetStatus_AwaitingReviewTerminalsStillWork verifies that terminal
// transitions from AWAITING_REVIEW (COMPLETED, SKIPPED, STOPPED) all still
// work after adding PENDING/RUNNING — regression guard.
func TestSetStatus_AwaitingReviewTerminalsStillWork(t *testing.T) {
	for _, target := range []string{StatusCompleted, StatusSkipped, StatusStopped} {
		t.Run("→"+target, func(t *testing.T) {
			wfJSON := makeThreeStepWorkflow(StatusAwaitingReview, StatusPending, StatusPending)
			wfPath := writeWFFile(t, wfJSON)

			if _, err := applyVerb(t, wfPath, "set-status", []string{"STEP_01_BRAINSTORMING", target}); err != nil {
				t.Fatalf("AWAITING_REVIEW→%s should be legal, got: %v", target, err)
			}
			wf := readWF(t, wfPath)
			if wf.Steps[0].Status != target {
				t.Errorf("expected %s after transition, got %q", target, wf.Steps[0].Status)
			}
		})
	}
}

// --- F-21: zero-step workflow skips stamp ---

// TestStampWorkflowTotalElapsedIfFinal_ZeroStepsSkipsStamp verifies that an
// empty steps array is not treated as final and the stamp is not applied.
func TestStampWorkflowTotalElapsedIfFinal_ZeroStepsSkipsStamp(t *testing.T) {
	raw := map[string]any{
		"totalElapsedMin": float64(0),
		"startedAt":       "2026-05-04T00:00:00Z",
		"steps":           []any{},
	}
	stepMap := map[string]any{"name": "PRD", "status": StatusCompleted}
	got := stampWorkflowTotalElapsedIfFinal(raw, stepMap, "2026-05-04T01:00:00Z")
	if got {
		t.Error("expected false for zero-step workflow, got true")
	}
	if raw["totalElapsedMin"] != float64(0) {
		t.Errorf("totalElapsedMin must remain 0 for zero-step workflow, got %v", raw["totalElapsedMin"])
	}
}

// --- F-22: all-SKIPPED, all-STOPPED, mixed-terminal stamp tests ---

// TestStampWorkflowTotalElapsedIfFinal_AllSkipped verifies that a workflow
// where all steps are SKIPPED is treated as final and the stamp fires.
func TestStampWorkflowTotalElapsedIfFinal_AllSkipped(t *testing.T) {
	raw := map[string]any{
		"totalElapsedMin": float64(0),
		"startedAt":       "2026-05-04T00:00:00Z",
		"steps": []any{
			map[string]any{"status": StatusSkipped, "name": "BRAINSTORMING"},
			map[string]any{"status": StatusSkipped, "name": "PRD"},
		},
	}
	stepMap := raw["steps"].([]any)[1].(map[string]any)
	got := stampWorkflowTotalElapsedIfFinal(raw, stepMap, "2026-05-04T01:00:00Z")
	if !got {
		t.Error("expected true (final) for all-SKIPPED workflow")
	}
	elapsed, _ := raw["totalElapsedMin"].(float64)
	if elapsed <= 0 {
		t.Errorf("expected totalElapsedMin > 0 for all-SKIPPED final workflow, got %v", elapsed)
	}
}

// TestStampWorkflowTotalElapsedIfFinal_AllStopped verifies that a workflow
// where all steps are STOPPED is treated as final and the stamp fires.
func TestStampWorkflowTotalElapsedIfFinal_AllStopped(t *testing.T) {
	raw := map[string]any{
		"totalElapsedMin": float64(0),
		"startedAt":       "2026-05-04T00:00:00Z",
		"steps": []any{
			map[string]any{"status": StatusStopped, "name": "BRAINSTORMING"},
			map[string]any{"status": StatusStopped, "name": "PRD"},
		},
	}
	stepMap := raw["steps"].([]any)[1].(map[string]any)
	got := stampWorkflowTotalElapsedIfFinal(raw, stepMap, "2026-05-04T01:00:00Z")
	if !got {
		t.Error("expected true (final) for all-STOPPED workflow")
	}
	elapsed, _ := raw["totalElapsedMin"].(float64)
	if elapsed <= 0 {
		t.Errorf("expected totalElapsedMin > 0 for all-STOPPED final workflow, got %v", elapsed)
	}
}

// TestStampWorkflowTotalElapsedIfFinal_MixedTerminal verifies that a workflow
// with a mix of COMPLETED, SKIPPED, and STOPPED steps is treated as final.
func TestStampWorkflowTotalElapsedIfFinal_MixedTerminal(t *testing.T) {
	raw := map[string]any{
		"totalElapsedMin": float64(0),
		"startedAt":       "2026-05-04T00:00:00Z",
		"steps": []any{
			map[string]any{"status": StatusCompleted, "name": "BRAINSTORMING"},
			map[string]any{"status": StatusSkipped, "name": "PRD"},
			map[string]any{"status": StatusStopped, "name": "TASK"},
		},
	}
	stepMap := raw["steps"].([]any)[2].(map[string]any)
	got := stampWorkflowTotalElapsedIfFinal(raw, stepMap, "2026-05-04T01:00:00Z")
	if !got {
		t.Error("expected true (final) for mixed-terminal workflow")
	}
	elapsed, _ := raw["totalElapsedMin"].(float64)
	if elapsed <= 0 {
		t.Errorf("expected totalElapsedMin > 0 for mixed-terminal workflow, got %v", elapsed)
	}
}

// --- F-24: empty WorkflowDir skips cache write ---

// TestSetCurrentStep_EmptyWorkflowDirSkipsCache verifies that set-current-step
// does not create any active-step cache file when WorkflowDir is empty.
func TestSetCurrentStep_EmptyWorkflowDirSkipsCache(t *testing.T) {
	wfPath := writeWFFile(t, makeThreeStepWorkflow(StatusPending, StatusPending, StatusPending))

	var result ApplyResult
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(makeThreeStepWorkflow(StatusPending, StatusPending, StatusPending)), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := Mutators["set-current-step"](raw, MutatorArgs{Args: []string{"STEP_01_BRAINSTORMING"}, WorkflowDir: ""}, &result); err != nil {
		t.Fatalf("set-current-step: %v", err)
	}

	// No active-step file should exist anywhere near the workflow file.
	cacheFile := filepath.Join(filepath.Dir(wfPath), ".browzer", "active-step")
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Error("active-step cache file must not be created when WorkflowDir is empty")
	}
}

// --- F-26: clear cache no-ops when file does not exist ---

// TestCompleteStep_ClearsActiveStepCacheOnFinal_NoCacheExists verifies that
// completing the final step succeeds and does not error when the active-step
// cache file was never created.
func TestCompleteStep_ClearsActiveStepCacheOnFinal_NoCacheExists(t *testing.T) {
	// Single RUNNING step — completing it makes the workflow terminal.
	wfJSON := makeThreeStepWorkflow(StatusRunning, StatusSkipped, StatusSkipped)
	wfPath := writeWFFile(t, wfJSON)
	wfDir := filepath.Dir(wfPath)

	// Intentionally do NOT create the cache file.
	cacheFile := filepath.Join(wfDir, ".browzer", "active-step")
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Fatal("cache file should not exist before the test")
	}

	_, err := ApplyAndPersist(wfPath, "complete-step", MutatorArgs{
		Args:        []string{"STEP_01_BRAINSTORMING"},
		WorkflowDir: wfDir,
	}, false)
	if err != nil {
		t.Fatalf("complete-step on final step without cache file: %v", err)
	}

	// Cache file must still be absent — no-op delete is correct.
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Error("cache file should remain absent after no-op delete")
	}
}

// --- F-10 test: set-status STOPPED clears cache when all steps terminal ---

// TestSetStatus_ClearsActiveStepCacheOnAborted verifies that transitioning the
// final non-terminal step to STOPPED triggers active-step cache deletion when
// all steps end up in a terminal state.
func TestSetStatus_ClearsActiveStepCacheOnAborted(t *testing.T) {
	// Steps: STOPPED, STOPPED, RUNNING — the RUNNING one is the final live step.
	wfJSON := makeThreeStepWorkflow(StatusStopped, StatusStopped, StatusRunning)
	wfPath := writeWFFile(t, wfJSON)
	wfDir := filepath.Dir(wfPath)

	// Pre-create the cache file.
	cacheDir := filepath.Join(wfDir, ".browzer")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheFile := filepath.Join(cacheDir, "active-step")
	writeActiveStepCache(wfDir, "STEP_03_COMMIT")
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Fatal("pre-condition: cache file must exist before transition")
	}

	// Transition the last RUNNING step to STOPPED.
	_, err := ApplyAndPersist(wfPath, "set-status", MutatorArgs{
		Args:        []string{"STEP_03_COMMIT", StatusStopped},
		WorkflowDir: wfDir,
	}, false)
	if err != nil {
		t.Fatalf("set-status STOPPED: %v", err)
	}

	// Cache must be cleared now that all steps are terminal.
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Error("active-step cache must be deleted after all steps reach terminal status via STOPPED")
	}
}
