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
	"time"
)

// timeParseRFC3339 is a thin wrapper around time.Parse for assertion
// readability. Returns the parsed time + parse error.
func timeParseRFC3339(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// mustParseRFC3339 calls t.Fatal on parse error so callers can shorten
// fixture-pinning bodies.
func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	out, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustParseRFC3339(%q): %v", s, err)
	}
	return out
}

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

	// StepBase TRULY-required fields (per workflow-v1.schema.json
	// `StepBase.required` AFTER the enrich-openapi-projection.mjs step
	// drops `*default | T` fields per WF-OPTIONAL-MARKER Phase 1.1).
	//
	// DOG-DESCRIBE-2 (2026-05-06): the previous expectation list
	// included every field declared in `#StepBase` regardless of
	// whether CUE marked it with an explicit default — that pinned
	// the old, broken `cue def --out openapi` projection which
	// over-stated `required[]`. The OPENAPI mirror is now aligned
	// with the CUE source semantics: defaulted fields (e.g.
	// `retryCount: *0 | int`, `owner: *null | string`,
	// `dispatches: *[] | [...]`) are NOT in `required[]` and the
	// scaffold correctly omits them so the operator sees the
	// minimum surface they MUST fill.
	wantBase := []string{
		"stepId",
		"name",
		"status",
		"applicability",
		"startedAt",
	}
	for _, f := range wantBase {
		if _, ok := skeleton[f]; !ok {
			t.Errorf("PRD scaffold missing StepBase field %q", f)
		}
	}

	// Defaulted fields MUST NOT appear in the scaffold (operator-
	// optional per WF-OPTIONAL-MARKER Phase 1.1). Pinning them here
	// flags any future drift back to the over-stated-required shape.
	wantOmitted := []string{
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
	for _, f := range wantOmitted {
		if _, ok := skeleton[f]; ok {
			t.Errorf("PRD scaffold should omit defaulted StepBase field %q", f)
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
	// startedAt is a `time.Format(time.RFC3339)` string. After
	// DOG-DESCRIBE-2 (2026-05-06) the scaffold honors `format:
	// "date-time"` and emits a current-UTC RFC3339 timestamp instead
	// of "" (which would fail CUE validation).
	startedAt, ok := skeleton["startedAt"].(string)
	if !ok {
		t.Fatalf("startedAt: got %v (%T), want string", skeleton["startedAt"], skeleton["startedAt"])
	}
	if startedAt == "" {
		t.Errorf("startedAt should be a RFC3339 timestamp, got empty string (DOG-DESCRIBE-2 regression)")
	}
	if _, parseErr := timeParseRFC3339(startedAt); parseErr != nil {
		t.Errorf("startedAt %q is not RFC3339-parseable: %v", startedAt, parseErr)
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

// TestScaffoldStep_DateTimeFormat_EmitsRFC3339 directly exercises the
// DOG-DESCRIBE-2 contract: a required string field with `format:
// "date-time"` must scaffold as a CUE-validatable RFC3339 timestamp.
// Without this fix the scaffold emitted `""` which fails the CUE
// `time.Format(time.RFC3339)` constraint, forcing every operator to
// hand-patch `startedAt` before `append-step` / `append-steps`.
func TestScaffoldStep_DateTimeFormat_EmitsRFC3339(t *testing.T) {
	// Pin the clock so the assertion is exact.
	prev := nowUTCFunc
	defer func() { nowUTCFunc = prev }()
	pinned := mustParseRFC3339(t, "2026-05-06T12:34:56Z")
	nowUTCFunc = func() time.Time { return pinned }

	out, err := ScaffoldStep("PRD")
	if err != nil {
		t.Fatal(err)
	}
	var skeleton map[string]any
	if err := json.Unmarshal(out, &skeleton); err != nil {
		t.Fatal(err)
	}
	startedAt, _ := skeleton["startedAt"].(string)
	if startedAt != "2026-05-06T12:34:56Z" {
		t.Errorf("startedAt: got %q, want %q (DOG-DESCRIBE-2)", startedAt, "2026-05-06T12:34:56Z")
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
