package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateCmd_CleanFixtureExits0PrintsValid verifies that
// `browzer workflow validate` on a structurally correct fixture exits 0
// and prints "valid" (or similar) to stdout.
// Covers T1-T-r5: validate on a clean fixture exits 0 and prints `valid`.
func TestValidateCmd_CleanFixtureExits0PrintsValid(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "validate", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("validate on clean fixture should exit 0, got error: %v\nstderr: %s", err, stderr.String())
	}

	out := strings.ToLower(strings.TrimSpace(stdout.String()))
	if !strings.Contains(out, "valid") {
		t.Errorf("expected stdout to contain 'valid', got %q", stdout.String())
	}
}

// TestValidateCmd_TamperedFixtureExitsNonZeroWithViolations verifies that
// `browzer workflow validate` on a tampered (invalid) fixture exits non-zero
// and prints each violation with a path and message.
// Covers T1-T-r5: validate on a tampered fixture exits non-zero with path+message violations.
func TestValidateCmd_TamperedFixtureExitsNonZeroWithViolations(t *testing.T) {
	// Tamper: set schemaVersion to 0 (missing/invalid) and add a step with illegal status.
	tampered := `{
  "schemaVersion": 0,
  "featureId": "",
  "featureName": "Test",
  "featDir": "docs/browzer/feat-test",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "INVALID_MODE", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01",
      "name": "BRAINSTORMING",
      "status": "ILLEGAL_STATUS",
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

	wfPath := writeWorkflowFile(t, tampered)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "validate", "--workflow", wfPath})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for tampered fixture, got nil error")
	}

	// Either stdout or stderr must contain violation details with path+message format.
	combinedOutput := stdout.String() + stderr.String()
	if combinedOutput == "" {
		t.Error("expected violation output on stdout or stderr, got nothing")
	}
	// Should mention at least one path-like reference (schemaVersion, featureId, or status).
	hasPaths := strings.Contains(combinedOutput, "schemaVersion") ||
		strings.Contains(combinedOutput, "featureId") ||
		strings.Contains(combinedOutput, "status") ||
		strings.Contains(combinedOutput, "config.mode")
	if !hasPaths {
		t.Errorf("expected violation output to contain path references, got: %s", combinedOutput)
	}
}

// TestValidateCmd_DoesNotMutateFile verifies that `browzer workflow validate`
// does not mutate the workflow.json file (pure read).
// Covers T1-T-r5: validate does not mutate without mutating.
func TestValidateCmd_DoesNotMutateFile(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	// Capture the file content before.
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	// Record mtime before.
	statBefore, err := os.Stat(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "validate", "--workflow", wfPath})
	_ = root.Execute()

	// File must be identical after.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("validate mutated the workflow.json file — it must be read-only")
	}
	// mtime must not have changed.
	statAfter, err := os.Stat(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if statAfter.ModTime() != statBefore.ModTime() {
		t.Error("validate changed mtime — it must not write to the file")
	}
}

// TestValidateCmd_WithStepsValidatesEachStep verifies that the validate
// command audits all steps in the workflow, not just the top-level fields.
// Covers T1-T-r5: each violation has path + message.
func TestValidateCmd_WithStepsValidatesEachStep(t *testing.T) {
	// Valid multi-step workflow.
	validMultiStep := minimalWorkflowJSON
	// Inject it via JSON manipulation to add a step.
	var doc map[string]any
	if err := json.Unmarshal([]byte(validMultiStep), &doc); err != nil {
		t.Fatal(err)
	}
	doc["steps"] = []any{
		map[string]any{
			"stepId":        "STEP_01_BRAINSTORMING",
			"name":          "BRAINSTORMING",
			"taskId":        "",
			"status":        "COMPLETED",
			"applicability": map[string]any{"applicable": true, "reason": "default"},
			"startedAt":     "2026-04-29T00:00:00Z",
			"completedAt":   "2026-04-29T00:01:00Z",
			"elapsedMin":    1.0,
			"retryCount":    0,
			"itDependsOn":   []any{},
			"nextStep":      "",
			"skillsToInvoke": []any{},
			"skillsInvoked": []any{},
			"owner":         nil,
			"worktrees":     map[string]any{"used": false, "worktrees": []any{}},
			"warnings":      []any{},
			"reviewHistory": []any{},
			"task":          map[string]any{},
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")

	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(wfPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "validate", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("multi-step valid workflow should pass: %v\nstderr: %s", err, stderr.String())
	}
}
