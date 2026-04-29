package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// TestPatch_JqExpressionApplied verifies that
// `browzer workflow patch --jq <expr>` applies an arbitrary jq mutation
// and the result is persisted under the same lock + validation pipeline.
// Covers T3-T-8 (happy path).
func TestPatch_JqExpressionApplied(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	// Set featureName via jq expression.
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.featureName = "Patched Feature Name"`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch with valid jq should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after patch: %v", err)
	}
	if doc.FeatureName != "Patched Feature Name" {
		t.Errorf("expected featureName='Patched Feature Name', got %q", doc.FeatureName)
	}
}

// TestPatch_SchemaViolatingMutationRejectedFileUnchanged verifies that a jq
// mutation that violates schema v1 (e.g. setting schemaVersion to 0) is
// rejected after validation and the original file is left unchanged.
// Covers T3-T-8 (schema rejection branch).
func TestPatch_SchemaViolatingMutationRejectedFileUnchanged(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	// Setting schemaVersion to 0 violates schema v1 validation.
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.schemaVersion = 0`,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for schema-violating patch, got nil error")
	}

	// File must be unchanged.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("schema-violating patch must not mutate workflow.json")
	}

	// Error output must mention the violation.
	combinedOutput := stdout.String() + stderr.String()
	if !strings.Contains(combinedOutput, "schemaVersion") &&
		!strings.Contains(strings.ToLower(combinedOutput), "schema") &&
		!strings.Contains(strings.ToLower(combinedOutput), "validation") {
		t.Errorf("expected error output to reference schema violation, got: %q", combinedOutput)
	}
}

// TestPatch_InvalidJqExpressionExitsNonZero verifies that an invalid/malformed
// jq expression exits non-zero without mutating the file.
// Covers T3-T-8 (jq parse error branch).
func TestPatch_InvalidJqExpressionExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	// Malformed jq expression.
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `THIS IS NOT VALID JQ !!!@@@`,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for invalid jq expression, got nil error")
	}

	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("invalid jq expression must not mutate workflow.json")
	}
}

// TestPatch_MissingJqFlagExitsNonZero verifies that calling patch without
// the --jq flag exits non-zero with a usage error.
// Covers T3-T-8 (missing required flag).
func TestPatch_MissingJqFlagExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit when --jq flag is missing, got nil error")
	}
}
