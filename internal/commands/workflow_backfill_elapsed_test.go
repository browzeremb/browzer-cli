package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// backfillFixtureJSON is a 3-step workflow shaped to exercise the matrix:
//   - STEP_01: completedAt + startedAt set, elapsedMin null     → fillable
//   - STEP_02: completedAt + startedAt set, elapsedMin already 7 → must NOT change
//   - STEP_03: completedAt missing                              → must skip
const backfillFixtureJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-backfill",
  "featureName": "Backfill Test",
  "featDir": "docs/browzer/feat-backfill",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T01:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_03",
  "nextStepId": "",
  "totalSteps": 3,
  "completedSteps": 2,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01",
      "name": "BRAINSTORMING",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": "2026-04-29T00:10:00Z",
      "elapsedMin": null,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {}
    },
    {
      "stepId": "STEP_02",
      "name": "PRD",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:10:00Z",
      "completedAt": "2026-04-29T00:25:00Z",
      "elapsedMin": 7,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {}
    },
    {
      "stepId": "STEP_03",
      "name": "TASKS_MANIFEST",
      "status": "RUNNING",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:25:00Z",
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {}
    }
  ]
}`

// readWorkflowMap reads workflow.json off disk into a generic map so the
// test can assert on raw fields (elapsedMin null vs 0 vs float).
func readWorkflowMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	return raw
}

func stepByID(t *testing.T, raw map[string]any, id string) map[string]any {
	t.Helper()
	steps, _ := raw["steps"].([]any)
	for _, s := range steps {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if sm["stepId"] == id {
			return sm
		}
	}
	t.Fatalf("step %s not found", id)
	return nil
}

func runBackfillElapsedT(t *testing.T, wfPath string, extra ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	args := []string{"workflow", "backfill-elapsed", "--workflow", wfPath}
	args = append(args, extra...)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// TestBackfillElapsed_MixedNullBackfill covers AC-18 mid-flow: a workflow
// with some completed steps lacking elapsedMin gets them filled in one pass,
// already-filled cells are preserved, and steps with missing completedAt
// are left alone.
func TestBackfillElapsed_MixedNullBackfill(t *testing.T) {
	wfPath := writeWorkflowFile(t, backfillFixtureJSON)

	if _, stderr, err := runBackfillElapsedT(t, wfPath); err != nil {
		t.Fatalf("backfill-elapsed should exit 0, got: %v\nstderr: %s", err, stderr)
	}

	raw := readWorkflowMap(t, wfPath)

	// STEP_01: was null → must now be 10 minutes.
	s1 := stepByID(t, raw, "STEP_01")
	v1, ok := s1["elapsedMin"].(float64)
	if !ok {
		t.Fatalf("STEP_01.elapsedMin: expected float64, got %T (%v)", s1["elapsedMin"], s1["elapsedMin"])
	}
	if v1 != 10 {
		t.Errorf("STEP_01.elapsedMin: expected 10, got %v", v1)
	}

	// STEP_02: was 7 (already filled) → must remain 7 even though raw
	// (completedAt - startedAt) would yield 15. Idempotency contract:
	// don't clobber operator-provided / previously-stamped values.
	s2 := stepByID(t, raw, "STEP_02")
	v2, _ := s2["elapsedMin"].(float64)
	if v2 != 7 {
		t.Errorf("STEP_02.elapsedMin: expected preserved 7, got %v", v2)
	}

	// STEP_03: missing completedAt → must remain at 0.
	s3 := stepByID(t, raw, "STEP_03")
	if v, _ := s3["elapsedMin"].(float64); v != 0 {
		t.Errorf("STEP_03.elapsedMin: expected 0 (skip), got %v", v)
	}

	// totalElapsedMin: sum of per-step values (10 + 7 = 17).
	tot, _ := raw["totalElapsedMin"].(float64)
	if tot != 17 {
		t.Errorf("totalElapsedMin: expected 17, got %v", tot)
	}
}

// TestBackfillElapsed_Idempotent covers AC-18: running the verb twice
// against the same workflow yields identical bytes-on-disk the second time.
func TestBackfillElapsed_Idempotent(t *testing.T) {
	wfPath := writeWorkflowFile(t, backfillFixtureJSON)

	if _, stderr, err := runBackfillElapsedT(t, wfPath); err != nil {
		t.Fatalf("first run: %v\nstderr: %s", err, stderr)
	}
	first, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := runBackfillElapsedT(t, wfPath); err != nil {
		t.Fatalf("second run: %v\nstderr: %s", err, stderr)
	}
	second, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	// Idempotency contract: a no-op mutation MUST leave bytes-on-disk
	// bit-identical (matches mutatorCompleteStep's already_completed
	// short-circuit).
	if !bytes.Equal(first, second) {
		t.Errorf("backfill-elapsed is not idempotent — second run changed the file:\nfirst:\n%s\nsecond:\n%s",
			first, second)
	}
}

// TestBackfillElapsed_AllNullBackfill covers AC-18 full-recovery: every
// completed step lacks elapsedMin (orchestrator died before Step 7 closure).
// One backfill pass populates them all and stamps totalElapsedMin.
func TestBackfillElapsed_AllNullBackfill(t *testing.T) {
	allNullJSON := `{
  "schemaVersion": 1,
  "featureId": "feat-allnull",
  "featureName": "All Null",
  "featDir": "docs/browzer/feat-allnull",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:30:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_02",
  "nextStepId": "",
  "totalSteps": 2,
  "completedSteps": 2,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01",
      "name": "BRAINSTORMING",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": "2026-04-29T00:05:00Z",
      "elapsedMin": null,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {}
    },
    {
      "stepId": "STEP_02",
      "name": "PRD",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:05:00Z",
      "completedAt": "2026-04-29T00:30:00Z",
      "elapsedMin": null,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {}
    }
  ]
}`
	wfPath := writeWorkflowFile(t, allNullJSON)

	if _, stderr, err := runBackfillElapsedT(t, wfPath); err != nil {
		t.Fatalf("backfill-elapsed: %v\nstderr: %s", err, stderr)
	}

	raw := readWorkflowMap(t, wfPath)
	v1, _ := stepByID(t, raw, "STEP_01")["elapsedMin"].(float64)
	if v1 != 5 {
		t.Errorf("STEP_01.elapsedMin: expected 5, got %v", v1)
	}
	v2, _ := stepByID(t, raw, "STEP_02")["elapsedMin"].(float64)
	if v2 != 25 {
		t.Errorf("STEP_02.elapsedMin: expected 25, got %v", v2)
	}
	tot, _ := raw["totalElapsedMin"].(float64)
	if tot != 30 {
		t.Errorf("totalElapsedMin: expected 30, got %v", tot)
	}
}

// TestBackfillElapsed_QuietClean covers AC-5 surface contract: --quiet
// silences the audit telemetry on stderr while still exiting 0 and
// updating the file on disk.
func TestBackfillElapsed_QuietClean(t *testing.T) {
	wfPath := writeWorkflowFile(t, backfillFixtureJSON)

	stdout, stderr, err := runBackfillElapsedT(t, wfPath, "--quiet")
	if err != nil {
		t.Fatalf("backfill-elapsed --quiet: %v\nstderr: %s", err, stderr)
	}
	// --quiet must keep the audit telemetry line off stderr. Errors would
	// still print, so a clean run produces empty stdout AND empty stderr.
	if stdout != "" {
		t.Errorf("--quiet expected empty stdout, got: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("--quiet expected empty stderr, got: %q", stderr)
	}

	// Sanity: --quiet did NOT skip the actual backfill.
	raw := readWorkflowMap(t, wfPath)
	if v, _ := stepByID(t, raw, "STEP_01")["elapsedMin"].(float64); v != 10 {
		t.Errorf("--quiet still must perform the mutation; STEP_01.elapsedMin=%v", v)
	}
}
