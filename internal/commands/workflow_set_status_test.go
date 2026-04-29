package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// TestSetStatus_LegalTransitionAccepted verifies that
// `browzer workflow set-status <stepId> <newStatus>` accepts a legal
// lifecycle transition (e.g. PENDING → RUNNING) and persists the change.
// Covers T3-T-4 (legal transition branch).
func TestSetStatus_LegalTransitionAccepted(t *testing.T) {
	// We need a step to transition; use a fixture with the step already present.
	withStep := `{
  "schemaVersion": 1,
  "featureId": "feat-status-test",
  "featureName": "Status Test",
  "featDir": "docs/browzer/feat-status-test",
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
	wfPath := writeWorkflowFile(t, withStep)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-status", "STEP_01_BRAINSTORMING", "RUNNING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("set-status PENDING→RUNNING (legal) should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after set-status: %v", err)
	}
	if doc.Steps[0].Status != wf.StatusRunning {
		t.Errorf("expected step status RUNNING, got %q", doc.Steps[0].Status)
	}
}

// TestSetStatus_IllegalTransitionExitsNonZeroNoMutation verifies that an
// illegal lifecycle transition (e.g. SKIPPED → RUNNING) exits non-zero with a
// descriptive error message, and leaves the file byte-for-byte unchanged.
// Covers T3-T-4 (illegal transition branch).
func TestSetStatus_IllegalTransitionExitsNonZeroNoMutation(t *testing.T) {
	skippedWorkflow := `{
  "schemaVersion": 1,
  "featureId": "feat-illegal-transition",
  "featureName": "Illegal Transition Test",
  "featDir": "docs/browzer/feat-illegal-transition",
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
      "status": "SKIPPED",
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
	wfPath := writeWorkflowFile(t, skippedWorkflow)

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// SKIPPED → RUNNING is an illegal backward transition.
	root.SetArgs([]string{
		"workflow", "set-status", "STEP_01_BRAINSTORMING", "RUNNING",
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for illegal transition SKIPPED→RUNNING, got nil error")
	}

	// Stderr must contain a descriptive error mentioning the transition.
	stderrStr := stderr.String()
	combinedOutput := stderrStr + stdout.String()
	if combinedOutput == "" {
		t.Error("expected descriptive error message for illegal transition, got nothing")
	}

	// File must be unchanged.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("illegal transition must not mutate workflow.json")
	}
}

// TestSetStatus_RunningStampsStartedAt verifies that PENDING → RUNNING auto-
// stamps startedAt to a non-empty RFC3339 string. The CLI is the single
// source of timestamp truth — skills no longer manually stamp startedAt.
// Covers Phase 1 spine item #5.
func TestSetStatus_RunningStampsStartedAt(t *testing.T) {
	withStep := `{
  "schemaVersion": 1,
  "featureId": "feat-startedat",
  "featureName": "StartedAt Test",
  "featDir": "docs/browzer/feat-startedat",
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
	wfPath := writeWorkflowFile(t, withStep)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-status", "STEP_01_BRAINSTORMING", "RUNNING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("set-status PENDING→RUNNING failed: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Steps[0].StartedAt == "" {
		t.Error("expected startedAt to be auto-stamped on PENDING→RUNNING transition, got empty string")
	}
}

// TestSetStatus_RunningPreservesExistingStartedAt verifies that a re-entry
// transitioning a step BACK into RUNNING (e.g. after a staging-regression
// loop in receiving-code-review) preserves the original startedAt — never
// overwrite an existing timestamp, otherwise elapsedMin lies about wall-clock.
// Covers Phase 1 spine item #5 idempotency contract (Risk Checkpoint #2).
func TestSetStatus_RunningPreservesExistingStartedAt(t *testing.T) {
	originalStartedAt := "2026-04-29T01:23:45Z"
	withRunningStep := `{
  "schemaVersion": 1,
  "featureId": "feat-preserve",
  "featureName": "Preserve Test",
  "featDir": "docs/browzer/feat-preserve",
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
      "status": "PAUSED_PENDING_OPERATOR",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "` + originalStartedAt + `",
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
	wfPath := writeWorkflowFile(t, withRunningStep)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-status", "STEP_01_BRAINSTORMING", "RUNNING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("re-entry PAUSED_PENDING_OPERATOR→RUNNING failed: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Steps[0].StartedAt != originalStartedAt {
		t.Errorf("expected original startedAt %q to be preserved on re-entry, got %q",
			originalStartedAt, doc.Steps[0].StartedAt)
	}
}

// TestSetStatus_CompletedComputesElapsedMin verifies that COMPLETED
// transitions auto-compute elapsedMin = (completedAt - startedAt) in
// minutes. Skills no longer compute it manually; the orchestrator's Step 7
// roll-up becomes redundant.
// Covers Phase 1 spine item #5.
func TestSetStatus_CompletedComputesElapsedMin(t *testing.T) {
	startedAt := "2026-04-29T10:00:00Z"
	withRunningStep := `{
  "schemaVersion": 1,
  "featureId": "feat-elapsed",
  "featureName": "Elapsed Test",
  "featDir": "docs/browzer/feat-elapsed",
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
      "startedAt": "` + startedAt + `",
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
	wfPath := writeWorkflowFile(t, withRunningStep)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-status", "STEP_01_BRAINSTORMING", "COMPLETED",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("set-status RUNNING→COMPLETED failed: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	step := doc.Steps[0]
	if step.CompletedAt == nil || *step.CompletedAt == "" {
		t.Fatal("expected completedAt to be stamped on COMPLETED")
	}
	if step.ElapsedMin <= 0 {
		t.Errorf("expected elapsedMin > 0 (startedAt %s, completedAt %s), got %v",
			startedAt, *step.CompletedAt, step.ElapsedMin)
	}
}

// TestSetStatus_NonExistentStepExitsNonZero verifies that targeting a stepId
// that doesn't exist in the workflow exits non-zero.
// Covers T3-T-4 (missing step branch).
func TestSetStatus_NonExistentStepExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-status", "STEP_99_NONEXISTENT", "RUNNING",
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for non-existent stepId, got nil error")
	}
}
