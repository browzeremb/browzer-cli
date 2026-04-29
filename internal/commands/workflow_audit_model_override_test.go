package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// taskWorkflowForModelOverride is a minimal workflow with a TASK step
// that has task.suggestedModel set.
const taskWorkflowForModelOverride = `{
  "schemaVersion": 1,
  "featureId": "feat-model-override",
  "featureName": "Model Override Test",
  "featDir": "docs/browzer/feat-model-override",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_05_TASK_01",
      "name": "TASK",
      "taskId": "TASK_01",
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
      "task": {
        "title": "Implement truncation audit mutator",
        "scope": ["packages/cli/internal/workflow/apply.go"],
        "suggestedModel": "haiku",
        "invariants": [],
        "reviewer": {},
        "explorer": {}
      }
    }
  ]
}`

// TestAuditModelOverride_WritesModelOverrideField verifies that
// `browzer workflow audit-model-override <stepId> <from> <to> <reason>`
// writes task.execution.modelOverride onto the step.
func TestAuditModelOverride_WritesModelOverrideField(t *testing.T) {
	wfPath := writeWorkflowFile(t, taskWorkflowForModelOverride)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "audit-model-override",
		"STEP_05_TASK_01", "haiku", "sonnet", "complexity-exceeded-haiku-threshold",
		"--workflow", wfPath, "--sync",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("audit-model-override: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	task := step["task"].(map[string]any)

	execRaw, ok := task["execution"]
	if !ok {
		t.Fatal("expected task.execution to be written, got nothing")
	}
	exec, ok := execRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected task.execution to be an object, got %T", execRaw)
	}
	overrideRaw, ok := exec["modelOverride"]
	if !ok {
		t.Fatal("expected task.execution.modelOverride to be written")
	}
	override, ok := overrideRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected modelOverride to be an object, got %T", overrideRaw)
	}
	if override["from"] != "haiku" {
		t.Errorf("expected modelOverride.from = haiku, got %q", override["from"])
	}
	if override["to"] != "sonnet" {
		t.Errorf("expected modelOverride.to = sonnet, got %q", override["to"])
	}
	if override["reason"] != "complexity-exceeded-haiku-threshold" {
		t.Errorf("expected modelOverride.reason, got %q", override["reason"])
	}
	if override["at"] == nil || override["at"] == "" {
		t.Error("expected modelOverride.at to be auto-stamped, got empty")
	}
}

// TestAuditModelOverride_NonExistentStepExitsNonZero verifies that targeting
// a stepId not present in the workflow exits non-zero.
func TestAuditModelOverride_NonExistentStepExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "audit-model-override",
		"STEP_99_NONEXISTENT", "haiku", "sonnet", "reason",
		"--workflow", wfPath, "--sync",
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for non-existent step, got nil")
	}
}

// TestAuditModelOverride_InsufficientArgsExitsNonZero verifies that omitting
// required args exits non-zero.
func TestAuditModelOverride_InsufficientArgsExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, taskWorkflowForModelOverride)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// Only 3 args instead of required 4.
	root.SetArgs([]string{
		"workflow", "audit-model-override",
		"STEP_05_TASK_01", "haiku", "sonnet",
		"--workflow", wfPath, "--sync",
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for missing reason arg, got nil")
	}
}
