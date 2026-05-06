// Package schema — describe_test.go
//
// WF-OPTIONAL-MARKER (2026-05-06): contract test for describe-step-type's
// `required` semantics. Asserts that:
//
//   - Fields declared `field?: T` in CUE return required: false.
//   - Fields with explicit `*default` in CUE return required: false.
//   - Fields with no `?`, no `*default`, and no nullable variant return
//     required: true.
//   - Optional ARRAY fields (`field?: [...T]`) emit BOTH a parent row
//     (required: false) AND element rows (whose required is governed
//     by the element type's own constraints).
//
// Without this test, the bug where every CUE-optional field was reported
// as required (because the walker only checked `Default()`, never the
// iterator's IsOptional()) would silently regress.
package schema

import (
	"encoding/json"
	"testing"
)

// fieldExpectation pairs a CUE schema path with the required bool we
// expect describe-step-type to emit. The test fails when describe-step-type
// either omits the path entirely or reports a different required value.
type fieldExpectation struct {
	StepType string // PRD, TASK, etc. — passed to DescribeStepType
	Path     string // exact path from describe-step-type output
	Required bool
	Reason   string // why we expect this — surfaces in failure output
}

func TestDescribeStepType_OptionalMarkerContract(t *testing.T) {
	cases := []fieldExpectation{
		// PRD: explicit `?` markers — must be optional.
		{StepType: "PRD", Path: "functionalRequirements[].text", Required: false, Reason: "CUE: text?"},
		{StepType: "PRD", Path: "functionalRequirements[].description", Required: false, Reason: "CUE: description?"},
		{StepType: "PRD", Path: "functionalRequirements[].priority", Required: false, Reason: "CUE: priority?"},
		{StepType: "PRD", Path: "acceptanceCriteria[].text", Required: false, Reason: "CUE: text?"},
		{StepType: "PRD", Path: "acceptanceCriteria[].description", Required: false, Reason: "CUE: description?"},
		{StepType: "PRD", Path: "nonFunctionalRequirements[].text", Required: false, Reason: "CUE: text?"},
		{StepType: "PRD", Path: "nonFunctionalRequirements[].description", Required: false, Reason: "CUE: description?"},
		{StepType: "PRD", Path: "nonFunctionalRequirements[].category", Required: false, Reason: "CUE: category?"},
		{StepType: "PRD", Path: "risks[].text", Required: false, Reason: "CUE: text?"},
		{StepType: "PRD", Path: "risks[].description", Required: false, Reason: "CUE: description?"},

		// PRD: no `?`, no `*default` — must be required.
		{StepType: "PRD", Path: "title", Required: true, Reason: "CUE: title: string"},
		{StepType: "PRD", Path: "functionalRequirements[].id", Required: true, Reason: "CUE: id: =~..."},
		{StepType: "PRD", Path: "acceptanceCriteria[].id", Required: true, Reason: "CUE: id: =~..."},
		{StepType: "PRD", Path: "personas[].id", Required: true, Reason: "CUE: id: =~..."},
		{StepType: "PRD", Path: "personas[].description", Required: true, Reason: "CUE: description: string"},
		{StepType: "PRD", Path: "successMetrics[].metric", Required: true, Reason: "CUE: metric: string"},
		{StepType: "PRD", Path: "successMetrics[].target", Required: true, Reason: "CUE: target: string"},
		{StepType: "PRD", Path: "successMetrics[].method", Required: true, Reason: "CUE: method: string"},
		{StepType: "PRD", Path: "risks[].mitigation", Required: true, Reason: "CUE: mitigation: string"},

		// PRD: explicit `*default` — must be optional (default supplied).
		{StepType: "PRD", Path: "personas", Required: false, Reason: "CUE: personas: *[] | [...]"},
		{StepType: "PRD", Path: "risks", Required: false, Reason: "CUE: risks: *[] | [...]"},
		{StepType: "PRD", Path: "successMetrics", Required: false, Reason: "CUE: successMetrics: *[] | [...]"},

		// PRD: no `?`, no `*default` — array parent must be required.
		{StepType: "PRD", Path: "functionalRequirements", Required: true, Reason: "CUE: functionalRequirements: [...]"},
		{StepType: "PRD", Path: "acceptanceCriteria", Required: true, Reason: "CUE: acceptanceCriteria: [...]"},

		// TASK: explicit `?` markers — must be optional.
		{StepType: "TASK", Path: "title", Required: false, Reason: "CUE: title?"},
		{StepType: "TASK", Path: "dependsOn", Required: false, Reason: "CUE: dependsOn?"},
		{StepType: "TASK", Path: "acceptanceCriteria", Required: false, Reason: "CUE: acceptanceCriteria?"},
		{StepType: "TASK", Path: "explorer", Required: false, Reason: "CUE: explorer?"},
		{StepType: "TASK", Path: "reviewer", Required: false, Reason: "CUE: reviewer?"},
		{StepType: "TASK", Path: "execution.gates.baseline.lint", Required: false, Reason: "CUE: lint?"},
		{StepType: "TASK", Path: "execution.gates.baseline.tests", Required: false, Reason: "CUE: tests?"},
		{StepType: "TASK", Path: "execution.gates.baseline.typecheck", Required: false, Reason: "CUE: typecheck?"},
		{StepType: "TASK", Path: "acceptanceCriteria[].description", Required: false, Reason: "CUE: description?"},

		// TASK: no `?`, no `*default` — must be required.
		{StepType: "TASK", Path: "scope", Required: true, Reason: "CUE: scope: [...string]"},
		{StepType: "TASK", Path: "execution", Required: true, Reason: "CUE: execution: #TaskExecutionResult"},
		{StepType: "TASK", Path: "acceptanceCriteria[].id", Required: true, Reason: "CUE: id: =~..."},
		{StepType: "TASK", Path: "dependsOn[]", Required: true, Reason: "CUE: element regex constraint"},
	}

	for _, stepType := range []string{"PRD", "TASK"} {
		out, err := DescribeStepType(stepType, DescribeOpts{JSON: true})
		if err != nil {
			t.Fatalf("DescribeStepType(%q): %v", stepType, err)
		}
		var fields []fieldInfo
		if err := json.Unmarshal([]byte(out), &fields); err != nil {
			t.Fatalf("unmarshal describe output for %q: %v", stepType, err)
		}
		byPath := make(map[string]fieldInfo, len(fields))
		for _, f := range fields {
			byPath[f.Path] = f
		}
		for _, c := range cases {
			if c.StepType != stepType {
				continue
			}
			f, ok := byPath[c.Path]
			if !ok {
				t.Errorf("%s: path %q not found in describe-step-type output (reason: %s)", stepType, c.Path, c.Reason)
				continue
			}
			if f.Required != c.Required {
				t.Errorf("%s.%s: got required=%v, want %v (reason: %s)",
					stepType, c.Path, f.Required, c.Required, c.Reason)
			}
		}
	}
}

// TestDescribeStepType_IncludeBase_PrefixesStepBaseRows asserts Phase 3.2
// (2026-05-06): when DescribeOpts.IncludeBase is set, the output includes
// every #StepBase wrapper field (stepId, name, status, applicability,
// startedAt, completedAt, …) under the `@base.` path prefix. The prefix
// keeps base rows from colliding with payload rows so existing consumers
// that filter by step-payload prefix (`prd.`, `task.`, …) keep working.
//
// Without the flag the output is unchanged — backward compat is the
// default.
func TestDescribeStepType_IncludeBase_PrefixesStepBaseRows(t *testing.T) {
	// Default (off): no @base. paths in output.
	off, err := DescribeStepType("PRD", DescribeOpts{JSON: true})
	if err != nil {
		t.Fatalf("describe PRD (off): %v", err)
	}
	var offFields []fieldInfo
	if err := json.Unmarshal([]byte(off), &offFields); err != nil {
		t.Fatalf("unmarshal off: %v", err)
	}
	for _, f := range offFields {
		if len(f.Path) >= 6 && f.Path[:6] == "@base." {
			t.Errorf("default output (IncludeBase=false) leaked @base.* row: %s", f.Path)
		}
	}

	// On: every StepBase required field present with @base. prefix.
	on, err := DescribeStepType("PRD", DescribeOpts{JSON: true, IncludeBase: true})
	if err != nil {
		t.Fatalf("describe PRD (on): %v", err)
	}
	var onFields []fieldInfo
	if err := json.Unmarshal([]byte(on), &onFields); err != nil {
		t.Fatalf("unmarshal on: %v", err)
	}
	have := map[string]bool{}
	for _, f := range onFields {
		have[f.Path] = true
	}
	wantBasePaths := []string{
		"@base.stepId",
		"@base.name",
		"@base.status",
		"@base.applicability",
		"@base.startedAt",
		"@base.completedAt",
		"@base.elapsedMin",
		"@base.retryCount",
		"@base.itDependsOn",
		"@base.nextStep",
		"@base.skillsToInvoke",
		"@base.skillsInvoked",
		"@base.owner",
		"@base.warnings",
		"@base.reviewHistory",
		"@base.dispatches",
	}
	for _, want := range wantBasePaths {
		if !have[want] {
			t.Errorf("--include-base output missing required #StepBase row %q", want)
		}
	}

	// Payload-side rows (e.g. `prd.title`) MUST still be present —
	// IncludeBase prepends, never replaces.
	if !have["prd.title"] && !have["title"] {
		t.Errorf("--include-base output dropped payload rows; expected `title` or `prd.title`")
	}

	// Sanity: --include-base output strictly grows the field set.
	if len(onFields) <= len(offFields) {
		t.Errorf("--include-base should add rows: off=%d on=%d", len(offFields), len(onFields))
	}
}

// TestDescribeStepType_ParentArrayRowAlwaysEmitted asserts that for every
// array-typed field in PRD and TASK schemas, the parent row (`<field>` with
// type=array) is present in the output — never silently absorbed into the
// element row alone. Without the parent row, consumers cannot answer "is
// the array field itself required in the payload?" — leading to spurious
// retries when CUE rejects (or accepts) a payload that omits an optional
// array.
func TestDescribeStepType_ParentArrayRowAlwaysEmitted(t *testing.T) {
	wantParents := map[string][]string{
		"PRD": {
			"personas",
			"objectives",
			"inScope",
			"outOfScope",
			"deliverables",
			"functionalRequirements",
			"nonFunctionalRequirements",
			"successMetrics",
			"acceptanceCriteria",
			"risks",
			"assumptions",
		},
		"TASK": {
			"scope",
			"dependsOn",
			"invariants",
			"acceptanceCriteria",
		},
	}
	for stepType, parents := range wantParents {
		out, err := DescribeStepType(stepType, DescribeOpts{JSON: true})
		if err != nil {
			t.Fatalf("DescribeStepType(%q): %v", stepType, err)
		}
		var fields []fieldInfo
		if err := json.Unmarshal([]byte(out), &fields); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		paths := make(map[string]string, len(fields))
		for _, f := range fields {
			paths[f.Path] = f.Type
		}
		for _, parent := range parents {
			typ, ok := paths[parent]
			if !ok {
				t.Errorf("%s: array parent row %q is missing from describe-step-type output", stepType, parent)
				continue
			}
			if typ != "array" {
				t.Errorf("%s.%s: parent row type=%q, want %q", stepType, parent, typ, "array")
			}
		}
	}
}
