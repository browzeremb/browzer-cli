package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
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

// TestStampWorkflowTotalElapsedIfFinal_StampsOnlyOnFinalStep verifies that
// totalElapsedMin stays 0 after completing steps 1 and 2 of a 3-step workflow,
// and is set to > 0 only when the final step completes.
// Tests stampWorkflowTotalElapsedIfFinal directly (complete-step mutator retired in v3.0.0).
func TestStampWorkflowTotalElapsedIfFinal_StampsOnlyOnFinalStep(t *testing.T) {
	now := "2026-05-01T10:10:00Z"
	makeRaw := func(s1, s2, s3 string) map[string]any {
		raw := map[string]any{}
		if err := json.Unmarshal([]byte(makeThreeStepWorkflow(s1, s2, s3)), &raw); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return raw
	}

	// Step 1 complete — not final.
	raw := makeRaw(StatusRunning, StatusRunning, StatusRunning)
	steps := raw["steps"].([]any)
	sm1 := steps[0].(map[string]any)
	sm1["status"] = StatusCompleted
	steps[0] = sm1
	raw["steps"] = steps
	got := stampWorkflowTotalElapsedIfFinal(raw, sm1, now)
	if got {
		t.Error("step 1: stampWorkflowTotalElapsedIfFinal should return false (not final)")
	}
	if v, _ := raw["totalElapsedMin"].(float64); v != 0 {
		t.Errorf("step 1: expected totalElapsedMin=0, got %v", v)
	}

	// Step 2 complete — not final.
	sm2 := steps[1].(map[string]any)
	sm2["status"] = StatusCompleted
	steps[1] = sm2
	raw["steps"] = steps
	got = stampWorkflowTotalElapsedIfFinal(raw, sm2, now)
	if got {
		t.Error("step 2: stampWorkflowTotalElapsedIfFinal should return false (not final)")
	}
	if v, _ := raw["totalElapsedMin"].(float64); v != 0 {
		t.Errorf("step 2: expected totalElapsedMin=0, got %v", v)
	}

	// Step 3 (COMMIT) complete — final step, totalElapsedMin must be stamped.
	sm3 := steps[2].(map[string]any)
	sm3["status"] = StatusCompleted
	sm3["name"] = StepCommit
	steps[2] = sm3
	raw["steps"] = steps
	raw["startedAt"] = "2026-05-01T10:00:00Z"
	got = stampWorkflowTotalElapsedIfFinal(raw, sm3, now)
	if !got {
		t.Error("step 3: stampWorkflowTotalElapsedIfFinal should return true (final)")
	}
	if v, _ := raw["totalElapsedMin"].(float64); v <= 0 {
		t.Errorf("step 3: expected totalElapsedMin > 0, got %v", v)
	}
}

// TestStampWorkflowTotalElapsedIfFinal_AllCompletedViaDirectCall verifies
// that stampWorkflowTotalElapsedIfFinal stamps totalElapsedMin > 0 only when
// the last step is also complete (all-terminal).
// (set-status mutator retired in v3.0.0; tests stampWorkflowTotalElapsedIfFinal directly.)
func TestStampWorkflowTotalElapsedIfFinal_AllCompletedViaDirectCall(t *testing.T) {
	now := "2026-05-01T10:10:00Z"
	raw := map[string]any{
		"totalElapsedMin": float64(0),
		"startedAt":       "2026-05-01T10:00:00Z",
		"steps": []any{
			map[string]any{"status": StatusCompleted, "name": "BRAINSTORMING"},
			map[string]any{"status": StatusCompleted, "name": "PRD"},
			map[string]any{"status": StatusRunning, "name": StepCommit},
		},
	}

	// Two completed, one running — not final.
	steps := raw["steps"].([]any)
	sm2 := steps[1].(map[string]any)
	got := stampWorkflowTotalElapsedIfFinal(raw, sm2, now)
	if got {
		t.Error("step 2: expected false (not all terminal)")
	}
	if v, _ := raw["totalElapsedMin"].(float64); v != 0 {
		t.Errorf("step 2: expected totalElapsedMin=0, got %v", v)
	}

	// All complete — final step triggers stamp.
	sm3 := steps[2].(map[string]any)
	sm3["status"] = StatusCompleted
	steps[2] = sm3
	raw["steps"] = steps
	got = stampWorkflowTotalElapsedIfFinal(raw, sm3, now)
	if !got {
		t.Error("step 3: expected true (all terminal)")
	}
	if v, _ := raw["totalElapsedMin"].(float64); v <= 0 {
		t.Errorf("step 3: expected totalElapsedMin > 0, got %v", v)
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

