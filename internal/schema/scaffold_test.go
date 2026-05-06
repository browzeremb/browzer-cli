// Package schema — scaffold_test.go
//
// Phase 3.1 (2026-05-06): contract tests for ScaffoldStep. Asserts the
// emitted skeleton:
//
//   - parses as valid JSON
//   - includes the StepBase wrapper required fields (stepId, name,
//     status, applicability, …)
//   - includes the per-step payload required field (e.g. `prd` for PRD)
//   - omits CUE-optional fields entirely
//   - is byte-identical across consecutive invocations (determinism)
//
// The test exists so a future schema change that breaks the
// scaffold contract surfaces as a clear test failure rather than as
// a silent regression in skill-driven dispatches.
package schema

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestScaffoldStep_PRD_HasStepBaseAndPayload asserts the PRD skeleton
// covers BOTH the StepBase wrapper fields and the `prd` payload.
func TestScaffoldStep_PRD_HasStepBaseAndPayload(t *testing.T) {
	out, err := ScaffoldStep("PRD")
	if err != nil {
		t.Fatalf("ScaffoldStep(PRD): %v", err)
	}

	var skeleton map[string]any
	if err := json.Unmarshal(out, &skeleton); err != nil {
		t.Fatalf("scaffold output is not valid JSON: %v\nraw: %s", err, string(out))
	}

	// StepBase fields (per workflow-v1.schema.json `StepBase.required`).
	wantBase := []string{
		"stepId",
		"name",
		"status",
		"applicability",
		"startedAt",
		"completedAt",
		"elapsedMin",
		"retryCount",
		"itDependsOn",
		"nextStep",
		"skillsToInvoke",
		"skillsInvoked",
		"owner",
		"warnings",
		"reviewHistory",
		"dispatches",
	}
	for _, f := range wantBase {
		if _, ok := skeleton[f]; !ok {
			t.Errorf("PRD scaffold missing StepBase field %q", f)
		}
	}

	// Per-step payload.
	if _, ok := skeleton["prd"]; !ok {
		t.Errorf("PRD scaffold missing `prd` payload field")
	}

	// Discriminator pinned.
	if got, _ := skeleton["name"].(string); got != "PRD" {
		t.Errorf("PRD scaffold: name = %q, want %q", got, "PRD")
	}
}

// TestScaffoldStep_OmitsOptionalFields asserts that CUE-optional
// fields (`field?: T`, `*default | T`) DO NOT appear in the
// skeleton. Without this guarantee the skeleton would over-state
// what the operator must supply.
func TestScaffoldStep_OmitsOptionalFields(t *testing.T) {
	out, err := ScaffoldStep("PRD")
	if err != nil {
		t.Fatalf("ScaffoldStep(PRD): %v", err)
	}
	var skeleton map[string]any
	if err := json.Unmarshal(out, &skeleton); err != nil {
		t.Fatal(err)
	}
	// `taskId` is StepBase-optional (`taskId?` in CUE) — not in
	// StepBase.required[] in JSON Schema. Must not appear.
	if _, ok := skeleton["taskId"]; ok {
		t.Errorf("PRD scaffold should omit optional StepBase field `taskId`")
	}
	// `worktrees` is StepBase-optional (`worktrees?` in CUE).
	if _, ok := skeleton["worktrees"]; ok {
		t.Errorf("PRD scaffold should omit optional StepBase field `worktrees`")
	}
	// `skipReason` is StepBase-optional.
	if _, ok := skeleton["skipReason"]; ok {
		t.Errorf("PRD scaffold should omit optional StepBase field `skipReason`")
	}
}

// TestScaffoldStep_TypeCorrectEmptyValues asserts every required
// field carries a type-correct empty value (string→"", array→[],
// object→{} or recursed, bool→false, int→0).
func TestScaffoldStep_TypeCorrectEmptyValues(t *testing.T) {
	out, err := ScaffoldStep("PRD")
	if err != nil {
		t.Fatal(err)
	}
	var skeleton map[string]any
	if err := json.Unmarshal(out, &skeleton); err != nil {
		t.Fatal(err)
	}

	// stepId is a string → ""
	if got, ok := skeleton["stepId"].(string); !ok || got != "" {
		t.Errorf("stepId: got %v (%T), want \"\"", skeleton["stepId"], skeleton["stepId"])
	}
	// itDependsOn is an array → []
	if arr, ok := skeleton["itDependsOn"].([]any); !ok || len(arr) != 0 {
		t.Errorf("itDependsOn: got %v (%T), want []", skeleton["itDependsOn"], skeleton["itDependsOn"])
	}
	// elapsedMin is a number → 0 (json.Unmarshal → float64(0))
	if got, ok := skeleton["elapsedMin"].(float64); !ok || got != 0 {
		t.Errorf("elapsedMin: got %v (%T), want 0", skeleton["elapsedMin"], skeleton["elapsedMin"])
	}
	// applicability is an object — must recurse into StepApplicability.
	app, ok := skeleton["applicability"].(map[string]any)
	if !ok {
		t.Fatalf("applicability: got %v (%T), want object", skeleton["applicability"], skeleton["applicability"])
	}
	// StepApplicability.required[] = [applicable]; reason is optional.
	if _, ok := app["applicable"].(bool); !ok {
		t.Errorf("applicability.applicable: got %v (%T), want bool", app["applicable"], app["applicable"])
	}
}

// TestScaffoldStep_AllStepTypes_Parse loops every entry in the
// allowlist and asserts (a) ScaffoldStep returns no error, (b) the
// output parses as JSON, and (c) the `name` discriminator matches
// the requested step type. Catches drift in the
// `stepNameToSchemaName` map.
func TestScaffoldStep_AllStepTypes_Parse(t *testing.T) {
	for _, stepType := range []string{
		"PRD",
		"TASKS_MANIFEST",
		"TASK",
		"BRAINSTORMING",
		"CODE_REVIEW",
		"RECEIVING_CODE_REVIEW",
		"WRITE_TESTS",
		"UPDATE_DOCS",
		"FEATURE_ACCEPTANCE",
		"COMMIT",
	} {
		t.Run(stepType, func(t *testing.T) {
			out, err := ScaffoldStep(stepType)
			if err != nil {
				t.Fatalf("ScaffoldStep(%q): %v", stepType, err)
			}
			var skeleton map[string]any
			if err := json.Unmarshal(out, &skeleton); err != nil {
				t.Fatalf("scaffold for %q is not valid JSON: %v\nraw: %s", stepType, err, string(out))
			}
			if got, _ := skeleton["name"].(string); got != stepType {
				t.Errorf("%s: name = %q, want %q", stepType, got, stepType)
			}
		})
	}
}

// TestScaffoldStep_Deterministic asserts byte-stable output across
// consecutive invocations. Required so daemon caching, snapshot tests,
// and operator-side `diff` workflows behave predictably.
func TestScaffoldStep_Deterministic(t *testing.T) {
	a, err := ScaffoldStep("TASK")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ScaffoldStep("TASK")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("scaffold output not deterministic:\nfirst:  %s\nsecond: %s", string(a), string(b))
	}
}

// TestScaffoldStep_UnknownStepType_ErrorsWithAllowlist asserts an
// invalid step name returns an error naming the allowlist so the
// caller can correct the typo.
func TestScaffoldStep_UnknownStepType_ErrorsWithAllowlist(t *testing.T) {
	_, err := ScaffoldStep("NOT_A_REAL_STEP")
	if err == nil {
		t.Fatal("expected error for unknown step type, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "NOT_A_REAL_STEP") {
		t.Errorf("error must name the bad input; got: %s", msg)
	}
	// At least one valid name must appear in the error.
	if !strings.Contains(msg, "PRD") || !strings.Contains(msg, "TASK") {
		t.Errorf("error must list the allowlist; got: %s", msg)
	}
}
