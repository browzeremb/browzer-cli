package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// twoStepWithNextWorkflowJSON is a workflow with two steps where
// STEP_01 has nextStep pointing to STEP_02.
const twoStepWithNextWorkflowJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-current-step-test",
  "featureName": "Current Step Test",
  "featDir": "docs/browzer/feat-current-step-test",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 2,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_BRAINSTORMING",
      "name": "BRAINSTORMING",
      "taskId": "",
      "status": "PENDING",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "",
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "STEP_02_PRD",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {}
    },
    {
      "stepId": "STEP_02_PRD",
      "name": "PRD",
      "taskId": "",
      "status": "PENDING",
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

// TestSetCurrentStep_UpdatesCurrentStepIdAndPropagatesNextStep verifies that
// `browzer workflow set-current-step STEP_01_BRAINSTORMING` sets currentStepId
// to STEP_01_BRAINSTORMING and propagates the step's nextStep into nextStepId.
// Covers T3-T-7 (happy path with nextStep propagation).
func TestSetCurrentStep_UpdatesCurrentStepIdAndPropagatesNextStep(t *testing.T) {
	wfPath := writeWorkflowFile(t, twoStepWithNextWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-current-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("set-current-step should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after set-current-step: %v", err)
	}

	if doc.CurrentStepID != "STEP_01_BRAINSTORMING" {
		t.Errorf("expected currentStepId=STEP_01_BRAINSTORMING, got %q", doc.CurrentStepID)
	}
	// nextStep of STEP_01_BRAINSTORMING is "STEP_02_PRD" — must be propagated.
	if doc.NextStepID != "STEP_02_PRD" {
		t.Errorf("expected nextStepId=STEP_02_PRD (from step.nextStep), got %q", doc.NextStepID)
	}
}

// TestSetCurrentStep_EmptyNextStepClearsNextStepId verifies that when the
// named step has an empty nextStep, the top-level nextStepId is cleared.
// Covers T3-T-7 (step with no nextStep).
func TestSetCurrentStep_EmptyNextStepClearsNextStepId(t *testing.T) {
	wfPath := writeWorkflowFile(t, twoStepWithNextWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	// STEP_02_PRD has nextStep="" so nextStepId should be cleared.
	root.SetArgs([]string{
		"workflow", "set-current-step", "STEP_02_PRD",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("set-current-step STEP_02_PRD should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after set-current-step: %v", err)
	}

	if doc.CurrentStepID != "STEP_02_PRD" {
		t.Errorf("expected currentStepId=STEP_02_PRD, got %q", doc.CurrentStepID)
	}
	if doc.NextStepID != "" {
		t.Errorf("expected nextStepId to be empty (step has no nextStep), got %q", doc.NextStepID)
	}
}

// TestSetCurrentStep_NonExistentStepExitsNonZero verifies that targeting a
// stepId that doesn't exist exits non-zero.
// Covers T3-T-7 (missing step branch).
func TestSetCurrentStep_NonExistentStepExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-current-step", "STEP_99_NONEXISTENT",
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for non-existent stepId, got nil error")
	}
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "STEP_99_NONEXISTENT") {
		t.Errorf("expected stderr to name missing stepId, got: %q", stderrStr)
	}
}
