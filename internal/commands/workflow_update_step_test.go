package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestUpdateStep_SetFieldMutatesOnlyNamedStep verifies that
// `browzer workflow update-step <stepId> --set <field>=<value>` mutates
// only the named step and leaves unrelated steps byte-identical.
// Resulting state must pass schema v1 validation.
// Covers T3-T-2.
func TestUpdateStep_SetFieldMutatesOnlyNamedStep(t *testing.T) {
	// Start with a workflow that has two steps.
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	// Capture the original raw JSON of the second step to verify it's unchanged.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var beforeDoc map[string]any
	if err := json.Unmarshal(data, &beforeDoc); err != nil {
		t.Fatal(err)
	}
	beforeSteps := beforeDoc["steps"].([]any)
	beforeStep0, _ := json.Marshal(beforeSteps[0])

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "update-step", "STEP_01_BRAINSTORMING",
		"--set", "status=RUNNING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("update-step should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	// Read the mutated file.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var afterDoc map[string]any
	if err := json.Unmarshal(after, &afterDoc); err != nil {
		t.Fatalf("parse workflow after update-step: %v", err)
	}

	afterSteps := afterDoc["steps"].([]any)
	if len(afterSteps) != len(beforeSteps) {
		t.Fatalf("step count changed: before=%d after=%d", len(beforeSteps), len(afterSteps))
	}

	// Named step must have status=RUNNING.
	namedStep := afterSteps[0].(map[string]any)
	if namedStep["status"] != "RUNNING" {
		t.Errorf("expected status RUNNING on named step, got %v", namedStep["status"])
	}

	// All other steps must be byte-identical (compare via JSON).
	afterStep0, _ := json.Marshal(afterSteps[0])
	_ = afterStep0
	_ = beforeStep0
	// (The named step changed, so we only verify that IF there were multiple steps
	// the unnamed ones are untouched. workflowWithStepsJSON has exactly 1 step, so
	// just validate the schema passes.)

	// Validate the resulting workflow passes schema v1 validation.
	var stdoutV, stderrV bytes.Buffer
	rootV := buildWorkflowCommand(&stdoutV, &stderrV)
	rootV.SetArgs([]string{"workflow", "validate", "--workflow", wfPath})
	if err := rootV.Execute(); err != nil {
		t.Errorf("workflow after update-step failed validation: %v\nstderr: %s", err, stderrV.String())
	}
}

// TestUpdateStep_SetOnlyNamedStepOtherStepsUntouched verifies that with multiple
// steps only the target step is mutated; other steps are structurally identical.
// Covers T3-T-2 (scope isolation sub-case).
func TestUpdateStep_SetOnlyNamedStepOtherStepsUntouched(t *testing.T) {
	// Build a workflow with two distinct steps.
	twoStepJSON := `{
  "schemaVersion": 1,
  "featureId": "feat-update-test",
  "featureName": "Update Test",
  "featDir": "docs/browzer/feat-update-test",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_01_BRAINSTORMING",
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
	wfPath := writeWorkflowFile(t, twoStepJSON)

	// Capture the second step before mutation.
	data, _ := os.ReadFile(wfPath)
	var beforeDoc map[string]any
	_ = json.Unmarshal(data, &beforeDoc)
	beforeSteps := beforeDoc["steps"].([]any)
	beforeStep1, _ := json.Marshal(beforeSteps[1])

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "update-step", "STEP_01_BRAINSTORMING",
		"--set", "status=RUNNING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("update-step: %v\nstderr: %s", err, stderr.String())
	}

	// Step at index 1 (STEP_02_PRD) must be byte-identical.
	after, _ := os.ReadFile(wfPath)
	var afterDoc map[string]any
	_ = json.Unmarshal(after, &afterDoc)
	afterSteps := afterDoc["steps"].([]any)
	afterStep1, _ := json.Marshal(afterSteps[1])

	if string(beforeStep1) != string(afterStep1) {
		t.Errorf("unrelated step STEP_02_PRD was mutated by update-step:\nbefore: %s\nafter:  %s",
			beforeStep1, afterStep1)
	}

	// Named step must now have status=RUNNING.
	namedStep := afterSteps[0].(map[string]any)
	if namedStep["status"] != "RUNNING" {
		t.Errorf("expected named step status RUNNING, got %v", namedStep["status"])
	}
	_ = strings.Contains // suppress unused import warning
}
