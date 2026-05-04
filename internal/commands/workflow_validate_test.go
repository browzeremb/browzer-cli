package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/schema"
)

// TestValidateCmd_CleanFixtureExits0PrintsValid verifies that
// `browzer workflow validate` on a structurally correct fixture exits 0
// and prints "valid" (or similar) to stdout.
// Covers T1-T-r5: validate on a clean fixture exits 0 and prints `valid`.
func TestValidateCmd_CleanFixtureExits0PrintsValid(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "validate", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("multi-step valid workflow should pass: %v\nstderr: %s", err, stderr.String())
	}
}

// validSchemaV2WorkflowJSON is a schema-v2 valid workflow fixture used by the
// new --json and --since-version tests. It uses the CUE-compliant featureId
// pattern and schemaVersion=2 so schema.ValidateWorkflow accepts it.
const validSchemaV2WorkflowJSON = `{
  "schemaVersion": 2,
  "pluginVersion": null,
  "featureId": "feat-20260504-validate-json-test",
  "featureName": "Validate JSON Test",
  "featDir": "docs/browzer/feat-20260504-validate-json-test",
  "originalRequest": "test fixture for --json flag",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-05-04T00:00:00Z"},
  "startedAt": "2026-05-04T00:00:00Z",
  "updatedAt": "2026-05-04T00:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}`

// invalidSchemaV2WorkflowJSON is a schema-v2 invalid workflow fixture:
// schemaVersion=1 triggers a violation at addedIn("2026-05-04T00:00:00Z")
// and the bad featureId pattern triggers one at addedIn("2026-04-24T00:00:00Z").
// This gives violations across two eras for --since-version filtering tests.
const invalidSchemaV2WorkflowJSON = `{
  "schemaVersion": 1,
  "pluginVersion": null,
  "featureId": "INVALID-ID",
  "featureName": "Invalid Fixture",
  "featDir": "docs/browzer/feat-invalid",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-05-04T00:00:00Z"},
  "startedAt": "2026-05-04T00:00:00Z",
  "updatedAt": "2026-05-04T00:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}`

// TestWorkflowValidate_JSONFlag verifies the --json flag behaviour:
// - valid fixture → JSON output with valid=true, violations=[], exit 0.
// - invalid fixture → JSON output with valid=false, non-empty violations, exit non-zero.
func TestWorkflowValidate_JSONFlag(t *testing.T) {
	t.Run("valid_workflow_emits_valid_json", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, validSchemaV2WorkflowJSON)
		// Clear BROWZER_NO_SCHEMA_CHECK so the CUE validator runs.
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		root.SetArgs([]string{"workflow", "validate", "--json", "--workflow", wfPath})

		err := root.Execute()
		if err != nil {
			t.Fatalf("expected exit 0 for valid workflow with --json, got error: %v\nstderr: %s", err, stderr.String())
		}

		var result schema.ValidationResult
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &result); jsonErr != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", jsonErr, stdout.String())
		}
		if !result.Valid {
			t.Errorf("expected valid=true for valid workflow, got false. violations: %+v", result.Violations)
		}
		if len(result.Violations) != 0 {
			t.Errorf("expected no violations for valid workflow, got %d: %+v", len(result.Violations), result.Violations)
		}
	})

	t.Run("invalid_workflow_emits_invalid_json_exits_nonzero", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, invalidSchemaV2WorkflowJSON)
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		root.SetArgs([]string{"workflow", "validate", "--json", "--workflow", wfPath})

		err := root.Execute()
		if err == nil {
			t.Error("expected non-zero exit for invalid workflow with --json, got nil error")
		}

		var result schema.ValidationResult
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &result); jsonErr != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", jsonErr, stdout.String())
		}
		if result.Valid {
			t.Error("expected valid=false for invalid workflow, got true")
		}
		if len(result.Violations) == 0 {
			t.Error("expected at least one violation for invalid workflow, got none")
		}
	})
}

// TestWorkflowValidate_SinceVersionFilter verifies --since-version filtering:
// - violations with addedIn < ts are excluded from output and exit code.
// - a future ts that filters ALL violations → exit 0 even though raw is invalid.
// - invalid RFC3339 → exit non-zero with clear error.
func TestWorkflowValidate_SinceVersionFilter(t *testing.T) {
	// The invalid fixture has schemaVersion=1 (addedIn 2026-05-04) and
	// INVALID-ID featureId pattern (addedIn 2026-04-24). Using since-version
	// 2026-05-04T00:00:00Z should keep schemaVersion violation but filter
	// the older featureId violation — leaving at least the newer one.

	t.Run("human_readable_filtered_exits_nonzero_when_violations_remain", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, invalidSchemaV2WorkflowJSON)
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		root.SetArgs([]string{"workflow", "validate", "--since-version", "2026-04-24T00:00:00Z", "--workflow", wfPath})

		err := root.Execute()
		if err == nil {
			t.Error("expected non-zero exit when violations remain after filtering")
		}
	})

	t.Run("json_output_filtered", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, invalidSchemaV2WorkflowJSON)
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		root.SetArgs([]string{"workflow", "validate", "--json", "--since-version", "2026-04-24T00:00:00Z", "--workflow", wfPath})

		_ = root.Execute()

		var result schema.ValidationResult
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &result); jsonErr != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", jsonErr, stdout.String())
		}
		// All violations should have addedIn >= 2026-04-24.
		for _, v := range result.Violations {
			if v.AddedIn < "2026-04-24T00:00:00Z" {
				t.Errorf("violation with addedIn %q should have been filtered by --since-version 2026-04-24", v.AddedIn)
			}
		}
	})

	t.Run("far_future_since_version_exits_zero_despite_invalid_workflow", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, invalidSchemaV2WorkflowJSON)
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		root.SetArgs([]string{"workflow", "validate", "--since-version", "2099-01-01T00:00:00Z", "--workflow", wfPath})

		err := root.Execute()
		if err != nil {
			t.Errorf("expected exit 0 when all violations filtered by far-future since-version, got: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}
		out := strings.TrimSpace(stdout.String())
		if !strings.Contains(strings.ToLower(out), "valid") {
			t.Errorf("expected 'valid' on stdout when all violations filtered, got %q", out)
		}
	})

	t.Run("invalid_rfc3339_exits_nonzero_with_clear_error", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, validSchemaV2WorkflowJSON)
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		root.SetArgs([]string{"workflow", "validate", "--since-version", "not-a-date", "--workflow", wfPath})

		err := root.Execute()
		if err == nil {
			t.Error("expected non-zero exit for invalid RFC3339 --since-version")
		}
		// Error message should mention the bad value and RFC3339.
		errMsg := err.Error()
		if !strings.Contains(errMsg, "not-a-date") && !strings.Contains(errMsg, "RFC3339") && !strings.Contains(errMsg, "since-version") {
			t.Errorf("expected error message to mention the bad timestamp or --since-version, got: %q", errMsg)
		}
	})
}

// TestWorkflowValidate_FlagsComposable verifies that --json and --since-version
// compose correctly: the output is JSON AND violations are filtered.
func TestWorkflowValidate_FlagsComposable(t *testing.T) {
	t.Run("json_and_since_version_compose", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, invalidSchemaV2WorkflowJSON)
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		// since-version far in the future: all violations filtered → valid=true in JSON.
		root.SetArgs([]string{"workflow", "validate", "--json", "--since-version", "2099-01-01T00:00:00Z", "--workflow", wfPath})

		err := root.Execute()
		if err != nil {
			t.Errorf("expected exit 0 when all violations filtered by far-future since-version with --json, got: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}

		// Verify JSON shape.
		var result schema.ValidationResult
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &result); jsonErr != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", jsonErr, stdout.String())
		}
		if !result.Valid {
			t.Errorf("expected valid=true after far-future filter, got false. violations: %+v", result.Violations)
		}
		if len(result.Violations) != 0 {
			t.Errorf("expected empty violations after far-future filter, got %d", len(result.Violations))
		}
	})

	t.Run("json_and_since_version_compose_with_remaining_violations", func(t *testing.T) {
		wfPath := writeWorkflowFile(t, invalidSchemaV2WorkflowJSON)
		t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		// since-version at epoch: all violations included → valid=false in JSON.
		root.SetArgs([]string{"workflow", "validate", "--json", "--since-version", "2000-01-01T00:00:00Z", "--workflow", wfPath})

		err := root.Execute()
		if err == nil {
			t.Error("expected non-zero exit when violations remain with --json + --since-version")
		}

		var result schema.ValidationResult
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &result); jsonErr != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", jsonErr, stdout.String())
		}
		if result.Valid {
			t.Error("expected valid=false when violations remain")
		}
		if len(result.Violations) == 0 {
			t.Error("expected non-empty violations when since-version is before all addedIn timestamps")
		}
	})
}

// TestWorkflowValidate_SinceVersionEmptyString verifies that
// `--since-version ""` is treated identically to omitting the flag
// (no filter applied). F-27 from receiving-code-review.
func TestWorkflowValidate_SinceVersionEmptyString(t *testing.T) {
	wfPath := writeWorkflowFile(t, validSchemaV2WorkflowJSON)
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")

	// Run with explicit empty string.
	var stdoutA, stderrA bytes.Buffer
	rootA := buildWorkflowCommand(&stdoutA, &stderrA)
	rootA.SetArgs([]string{"workflow", "validate", "--json", "--since-version", "", "--workflow", wfPath})
	if err := rootA.Execute(); err != nil {
		t.Fatalf("--since-version \"\" against valid workflow: expected exit 0, got %v\nstderr: %s", err, stderrA.String())
	}

	// Run without the flag at all.
	var stdoutB, stderrB bytes.Buffer
	rootB := buildWorkflowCommand(&stdoutB, &stderrB)
	rootB.SetArgs([]string{"workflow", "validate", "--json", "--workflow", wfPath})
	if err := rootB.Execute(); err != nil {
		t.Fatalf("validate --json without --since-version: expected exit 0, got %v\nstderr: %s", err, stderrB.String())
	}

	// Both outputs must contain the same valid=true + zero violations result.
	var resA, resB schema.ValidationResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdoutA.String())), &resA); err != nil {
		t.Fatalf("parse A: %v", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdoutB.String())), &resB); err != nil {
		t.Fatalf("parse B: %v", err)
	}
	if resA.Valid != resB.Valid || len(resA.Violations) != len(resB.Violations) {
		t.Errorf("--since-version \"\" should equal no-flag; got A=%+v B=%+v", resA, resB)
	}
}

// TestWorkflowValidate_JSONFlag_MalformedWorkflowFile verifies that
// `validate --json` against a non-JSON-parseable workflow file emits a
// structured ValidationResult to stdout (not unstructured stderr).
// F-28 from receiving-code-review.
func TestWorkflowValidate_JSONFlag_MalformedWorkflowFile(t *testing.T) {
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "bad-workflow.json")
	if err := os.WriteFile(wfPath, []byte(`{this is not json`), 0o644); err != nil {
		t.Fatalf("write malformed workflow: %v", err)
	}
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "validate", "--json", "--workflow", wfPath})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit when workflow.json is malformed")
	}

	var result schema.ValidationResult
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &result); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON for malformed workflow: %v\nstdout: %s", jsonErr, stdout.String())
	}
	if result.Valid {
		t.Error("expected valid=false for malformed workflow input")
	}
	if len(result.Violations) == 0 {
		t.Error("expected at least 1 violation describing the JSON parse error")
	}
}
