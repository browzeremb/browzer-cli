package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// helper: build a fresh workflow command tree and execute args, capturing stdout+stderr.
func runWorkflowSchemaCmd(t *testing.T, args ...string) (stdout string, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer

	root := &cobra.Command{Use: "browzer"}
	registerWorkflow(root)
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"workflow", "schema"}, args...))

	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestSchema_DefaultOutputContainsTopLevelFields(t *testing.T) {
	out, _, err := runWorkflowSchemaCmd(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, field := range []string{"schemaVersion", "featureId", "steps"} {
		if !strings.Contains(out, field) {
			t.Errorf("default output missing field %q\noutput:\n%s", field, out)
		}
	}
}

func TestSchema_JSONSchemaFlagOutputsValidJSON(t *testing.T) {
	out, _, err := runWorkflowSchemaCmd(t, "--json-schema")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &m); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", jsonErr, out)
	}
	if _, ok := m["$schema"]; !ok {
		t.Errorf("missing $schema field in JSON Schema output")
	}
	// Verify steps.items.$ref
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		t.Fatal("missing properties in schema")
	}
	stepsSchema, _ := props["steps"].(map[string]any)
	if stepsSchema == nil {
		t.Fatal("missing steps in properties")
	}
	items, _ := stepsSchema["items"].(map[string]any)
	if items == nil {
		t.Fatal("missing items in steps schema")
	}
	ref, _ := items["$ref"].(string)
	if ref != "#/$defs/Step" {
		t.Errorf("expected steps.items.$ref == \"#/$defs/Step\", got %q", ref)
	}
}

func TestSchema_JSONSchemaContainsAllStepNames(t *testing.T) {
	out, _, err := runWorkflowSchemaCmd(t, "--json-schema")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &m); jsonErr != nil {
		t.Fatalf("invalid JSON: %v", jsonErr)
	}
	defs, _ := m["$defs"].(map[string]any)
	if defs == nil {
		t.Fatal("missing $defs")
	}
	stepDef, _ := defs["Step"].(map[string]any)
	if stepDef == nil {
		t.Fatal("missing Step in $defs")
	}
	stepProps, _ := stepDef["properties"].(map[string]any)
	if stepProps == nil {
		t.Fatal("missing properties in Step")
	}
	nameProp, _ := stepProps["name"].(map[string]any)
	if nameProp == nil {
		t.Fatal("missing name in Step properties")
	}
	rawEnum, _ := nameProp["enum"].([]any)
	if rawEnum == nil {
		t.Fatal("missing enum in Step.name")
	}
	enumSet := make(map[string]bool, len(rawEnum))
	for _, v := range rawEnum {
		s, _ := v.(string)
		enumSet[s] = true
	}
	expected := []string{
		"BRAINSTORMING", "PRD", "TASKS_MANIFEST", "TASK",
		"CODE_REVIEW", "RECEIVING_CODE_REVIEW", "WRITE_TESTS",
		"UPDATE_DOCS", "FEATURE_ACCEPTANCE", "COMMIT", "FIX_FINDINGS",
	}
	for _, name := range expected {
		if !enumSet[name] {
			t.Errorf("step name %q missing from enum", name)
		}
	}
}

func TestSchema_FieldFlagScopesToSubpath(t *testing.T) {
	out, _, err := runWorkflowSchemaCmd(t, "--field", "steps.items", "--json-schema")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &m); jsonErr != nil {
		t.Fatalf("invalid JSON: %v", jsonErr)
	}
	// Should NOT contain top-level workflow fields like schemaVersion.
	if props, ok := m["properties"].(map[string]any); ok {
		if _, hasSchemaVersion := props["schemaVersion"]; hasSchemaVersion {
			t.Error("scoped output should not contain top-level 'schemaVersion'")
		}
	}
	// Should be a $ref pointing to Step or directly contain Step def
	if _, hasRef := m["$ref"]; !hasRef {
		// also acceptable: direct Step definition with 'required'
		if _, hasReq := m["required"]; !hasReq {
			t.Error("scoped output for steps.items should contain $ref or required")
		}
	}
}

func TestSchema_FieldFlagUnknownPathExitsNonZero(t *testing.T) {
	_, _, err := runWorkflowSchemaCmd(t, "--field", "nonexistent.path")
	if err == nil {
		t.Fatal("expected non-zero exit for unknown field path, got nil error")
	}
}
