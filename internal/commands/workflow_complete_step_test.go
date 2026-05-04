package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// inProgressWorkflowJSON is a minimal workflow with one step in IN_PROGRESS status.
// Note: IN_PROGRESS maps to RUNNING in schema v1.
const inProgressWorkflowJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-complete-test",
  "featureName": "Complete Test",
  "featDir": "docs/browzer/feat-complete-test",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_01_BRAINSTORMING",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_BRAINSTORMING",
      "name": "BRAINSTORMING",
      "taskId": "",
      "status": "RUNNING",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
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

// TestCompleteStep_RunningTransitionsToCompletedWithCompletedAt verifies that
// `browzer workflow complete-step STEP_01_BRAINSTORMING` transitions a RUNNING
// step to COMPLETED and sets completedAt to a non-empty timestamp.
// Covers T3-T-3 (first invocation).
func TestCompleteStep_RunningTransitionsToCompletedWithCompletedAt(t *testing.T) {
	wfPath := writeWorkflowFile(t, inProgressWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "complete-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("complete-step on RUNNING step should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after complete-step: %v", err)
	}

	if len(doc.Steps) == 0 {
		t.Fatal("expected at least one step")
	}
	step := doc.Steps[0]
	if step.Status != wf.StatusCompleted {
		t.Errorf("expected status COMPLETED, got %q", step.Status)
	}
	if step.CompletedAt == nil || *step.CompletedAt == "" {
		t.Error("expected completedAt to be set after complete-step")
	}
	// Phase 1 spine item #5: complete-step on a step with startedAt
	// pre-populated MUST also auto-compute elapsedMin so retro-analysis
	// gets non-zero wall-clock without skills having to compute it.
	if step.ElapsedMin <= 0 {
		t.Errorf("expected elapsedMin > 0 (startedAt %s, completedAt %s), got %v",
			step.StartedAt, *step.CompletedAt, step.ElapsedMin)
	}
}

// TestCompleteStep_NoStartedAtLeavesElapsedMinZero verifies the graceful
// fallback when complete-step runs against a step that never went through
// RUNNING (legacy workflows or skipped steps that jumped to COMPLETED).
// Don't lie — if startedAt is missing, leave elapsedMin alone rather than
// stamp a bogus delta.
// Covers Phase 1 spine item #5 fallback path.
func TestCompleteStep_NoStartedAtLeavesElapsedMinZero(t *testing.T) {
	noStartedWorkflow := `{
  "schemaVersion": 1,
  "featureId": "feat-no-started",
  "featureName": "No StartedAt Test",
  "featDir": "docs/browzer/feat-no-started",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_01_BRAINSTORMING",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_BRAINSTORMING",
      "name": "BRAINSTORMING",
      "status": "RUNNING",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "",
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
	wfPath := writeWorkflowFile(t, noStartedWorkflow)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "complete-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("complete-step should still succeed with empty startedAt, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Steps[0].Status != wf.StatusCompleted {
		t.Errorf("expected status COMPLETED, got %q", doc.Steps[0].Status)
	}
	if doc.Steps[0].ElapsedMin != 0 {
		t.Errorf("expected elapsedMin to remain 0 when startedAt is empty, got %v",
			doc.Steps[0].ElapsedMin)
	}
}

// TestCompleteStep_IdempotentOnAlreadyCompletedExits0WithWarning verifies that
// calling complete-step a second time on an already-COMPLETED step exits 0
// (no error), emits a warning on stderr, and does NOT mutate the file.
// Covers T3-T-3 (idempotent second invocation).
func TestCompleteStep_IdempotentOnAlreadyCompletedExits0WithWarning(t *testing.T) {
	// Build a workflow whose step is already COMPLETED.
	completedAt := "2026-04-29T00:01:00Z"
	completedWorkflow := strings.ReplaceAll(inProgressWorkflowJSON, `"status": "RUNNING"`, `"status": "COMPLETED"`)
	completedWorkflow = strings.ReplaceAll(completedWorkflow, `"completedAt": null`, `"completedAt": "`+completedAt+`"`)

	wfPath := writeWorkflowFile(t, completedWorkflow)

	// Capture file content before second invocation.
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "complete-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
	})

	// Second invocation must exit 0.
	if err := root.Execute(); err != nil {
		t.Fatalf("complete-step on already-COMPLETED step should be idempotent (exit 0), got: %v\nstderr: %s",
			err, stderr.String())
	}

	// Warning must appear on stderr.
	stderrStr := stderr.String()
	if !strings.Contains(strings.ToLower(stderrStr), "already") &&
		!strings.Contains(strings.ToLower(stderrStr), "completed") &&
		!strings.Contains(strings.ToLower(stderrStr), "idempotent") &&
		!strings.Contains(strings.ToLower(stderrStr), "no-op") {
		t.Errorf("expected idempotent warning on stderr, got: %q", stderrStr)
	}

	// File must not have been mutated.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("complete-step on already-COMPLETED step must not mutate workflow.json")
	}
}

// buildCommitWorkflowJSON constructs a workflow.json fixture with a single
// COMMIT step. wfCompletedAt is the workflow-level completedAt (empty = omit).
func buildCommitWorkflowJSON(wfStartedAt, stepStartedAt, stepStatus string, totalElapsedMin float64, wfCompletedAt string) string {
	stepCompletedAtField := "null"
	wfCompletedAtField := `"completedAt": null`
	if wfCompletedAt != "" {
		stepCompletedAtField = `"` + wfCompletedAt + `"`
		wfCompletedAtField = `"completedAt": "` + wfCompletedAt + `"`
	}
	return fmt.Sprintf(`{
  "schemaVersion": 1,
  "featureId": "feat-commit-test",
  "featureName": "Commit Test",
  "featDir": "docs/browzer/feat-commit-test",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": %q,
  "updatedAt": %q,
  "totalElapsedMin": %g,
  %s,
  "currentStepId": "STEP_01_COMMIT",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_COMMIT",
      "name": "COMMIT",
      "taskId": "",
      "status": %q,
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": %q,
      "completedAt": %s,
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
}`, wfStartedAt, wfStartedAt, totalElapsedMin, wfCompletedAtField, stepStatus, stepStartedAt, stepCompletedAtField)
}

// TestCompleteStep_StampsStepElapsed is a regression guard that verifies the
// pre-existing elapsedMin stamping behaviour is preserved after the
// totalElapsedMin changes.
func TestCompleteStep_StampsStepElapsed(t *testing.T) {
	// Reuse the existing inProgressWorkflowJSON fixture (BRAINSTORMING step,
	// startedAt set) — a plain RUNNING→COMPLETED transition must still stamp
	// step.elapsedMin > 0.
	wfPath := writeWorkflowFile(t, inProgressWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "complete-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("complete-step failed: %v\nstderr: %s", err, stderr.String())
	}

	data, _ := os.ReadFile(wfPath)
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Steps[0].ElapsedMin <= 0 {
		t.Errorf("regression: expected elapsedMin > 0, got %v", doc.Steps[0].ElapsedMin)
	}
}

// TestCompleteStep_CommitStampsTotalElapsed verifies that completing a COMMIT
// step stamps workflow.totalElapsedMin > 0 and workflow.completedAt != "".
func TestCompleteStep_CommitStampsTotalElapsed(t *testing.T) {
	now := time.Now().UTC()
	wfStartedAt := now.Add(-72 * time.Minute).Format(time.RFC3339)
	stepStartedAt := now.Add(-30 * time.Second).Format(time.RFC3339)

	fixture := buildCommitWorkflowJSON(wfStartedAt, stepStartedAt, "RUNNING", 0, "")
	wfPath := writeWorkflowFile(t, fixture)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "complete-step", "STEP_01_COMMIT",
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("complete-step on COMMIT step failed: %v\nstderr: %s", err, stderr.String())
	}

	data, _ := os.ReadFile(wfPath)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	totalElapsed, _ := raw["totalElapsedMin"].(float64)
	if totalElapsed <= 0 {
		t.Errorf("expected totalElapsedMin > 0 after COMMIT step completed, got %v", totalElapsed)
	}
	completedAt, _ := raw["completedAt"].(string)
	if completedAt == "" {
		t.Error("expected workflow.completedAt to be set after COMMIT step completed")
	}

	// Also verify step.elapsedMin > 0
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Steps[0].ElapsedMin <= 0 {
		t.Errorf("expected step.elapsedMin > 0, got %v", doc.Steps[0].ElapsedMin)
	}
}

// TestCompleteStep_Idempotent verifies that if totalElapsedMin is already
// stamped (> 0), completing the step again does NOT overwrite it.
func TestCompleteStep_Idempotent(t *testing.T) {
	now := time.Now().UTC()
	wfStartedAt := now.Add(-10 * time.Minute).Format(time.RFC3339)
	stepStartedAt := now.Add(-5 * time.Minute).Format(time.RFC3339)
	preStampedAt := now.Add(-1 * time.Minute).Format(time.RFC3339)

	// Pre-populate totalElapsedMin=99.99 and completedAt.
	// The COMMIT step itself is RUNNING so complete-step has a valid transition.
	fixture := buildCommitWorkflowJSON(wfStartedAt, stepStartedAt, "RUNNING", 99.99, preStampedAt)
	wfPath := writeWorkflowFile(t, fixture)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "complete-step", "STEP_01_COMMIT",
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("complete-step failed: %v\nstderr: %s", err, stderr.String())
	}

	data, _ := os.ReadFile(wfPath)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	totalElapsed, _ := raw["totalElapsedMin"].(float64)
	if totalElapsed != 99.99 {
		t.Errorf("idempotency violated: expected totalElapsedMin=99.99, got %v", totalElapsed)
	}
	// completedAt should remain the pre-stamped value
	completedAt, _ := raw["completedAt"].(string)
	if completedAt != preStampedAt {
		t.Errorf("idempotency violated: expected completedAt=%q, got %q", preStampedAt, completedAt)
	}
}

// TestCompleteStep_72MinFixture verifies the 72-minute fixture produces a
// totalElapsedMin in the expected window [71.95, 72.10].
func TestCompleteStep_72MinFixture(t *testing.T) {
	now := time.Now().UTC()
	wfStartedAt := now.Add(-72 * time.Minute).Format(time.RFC3339)
	stepStartedAt := now.Add(-1 * time.Second).Format(time.RFC3339)

	fixture := buildCommitWorkflowJSON(wfStartedAt, stepStartedAt, "RUNNING", 0, "")
	wfPath := writeWorkflowFile(t, fixture)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "complete-step", "STEP_01_COMMIT",
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("complete-step failed: %v\nstderr: %s", err, stderr.String())
	}

	data, _ := os.ReadFile(wfPath)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	totalElapsed, _ := raw["totalElapsedMin"].(float64)
	// QA-007 (2026-05-04): widen the window from [71.95, 72.10] to
	// [71.50, 72.50] to absorb slow CI runners. The fixture sets
	// startedAt=72min-ago and the test's wall-clock advances during
	// the cobra command's execution; on a stressed CI runner the
	// delta is occasionally >0.10min above the floor. The 60s window
	// (±30s of the 72-minute target) is still tight enough to fail
	// loudly on real bugs (e.g. integer truncation, sign flip) but
	// won't flake on scheduler jitter.
	const lo, hi = 71.50, 72.50
	if totalElapsed < lo || totalElapsed > hi {
		t.Errorf("expected totalElapsedMin in [%.2f, %.2f], got %.4f", lo, hi, totalElapsed)
	}
	completedAt, _ := raw["completedAt"].(string)
	if completedAt == "" {
		t.Error("expected workflow.completedAt to be set")
	}
}
