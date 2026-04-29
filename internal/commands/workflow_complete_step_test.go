package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

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
