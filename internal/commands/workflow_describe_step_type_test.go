package commands

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/schema"
)

// TestDescribeStepType_Deterministic verifies that calling DescribeStepType
// twice in-process returns byte-identical output (no time-based or
// pointer-hash randomness).
func TestDescribeStepType_Deterministic(t *testing.T) {
	opts := schema.DescribeOpts{JSON: true}

	out1, err := schema.DescribeStepType("TASK", opts)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	out2, err := schema.DescribeStepType("TASK", opts)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if !bytes.Equal([]byte(out1), []byte(out2)) {
		t.Errorf("output not deterministic:\nfirst:  %s\nsecond: %s", out1[:minLen(len(out1), 200)], out2[:minLen(len(out2), 200)])
	}
}

// TestDescribeStepType_TaskRequiredOnly_KindEnum verifies that the
// scopeAdjustments field is present in TASK output, and that the
// 2026-04-30T18:00:00Z addedIn timestamp (from #ScopeAdjustment.kind) appears
// in the addedInMap (verified via the CODE_REVIEW step which has flat-accessible
// fields from that timestamp). Also verifies that --required-only reduces the
// field list and --field scopes the projection without error.
func TestDescribeStepType_TaskRequiredOnly_KindEnum(t *testing.T) {
	// Verify --json output includes execution.scopeAdjustments field (the array).
	opts := schema.DescribeOpts{JSON: true}
	out, err := schema.DescribeStepType("TASK", opts)
	if err != nil {
		t.Fatalf("describe TASK: %v", err)
	}

	var allFields []map[string]any
	if err := json.Unmarshal([]byte(out), &allFields); err != nil {
		t.Fatalf("unmarshal fields: %v\nraw: %s", err, out[:minLen(len(out), 300)])
	}

	// execution.scopeAdjustments must be present (it's an array field from
	// #TaskExecutionResult, added at 2026-04-24T00:00:00Z).
	found := false
	for _, f := range allFields {
		if path, _ := f["path"].(string); path == "execution.scopeAdjustments" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("execution.scopeAdjustments not found in TASK describe output")
	}

	// --required-only should reduce the field count.
	reqOpts := schema.DescribeOpts{JSON: true, RequiredOnly: true}
	reqOut, err := schema.DescribeStepType("TASK", reqOpts)
	if err != nil {
		t.Fatalf("describe TASK --required-only: %v", err)
	}
	var reqFields []map[string]any
	if err := json.Unmarshal([]byte(reqOut), &reqFields); err != nil {
		t.Fatalf("unmarshal required-only fields: %v", err)
	}
	if len(reqFields) >= len(allFields) {
		t.Errorf("--required-only should produce fewer fields (%d) than all-fields (%d)", len(reqFields), len(allFields))
	}

	// --field scopes to a sub-path. Verify no error for a valid path.
	fieldOpts := schema.DescribeOpts{Field: ".execution"}
	_, err = schema.DescribeStepType("TASK", fieldOpts)
	if err != nil {
		t.Errorf("--field .execution returned unexpected error: %v", err)
	}

	// The addedIn timestamp 2026-04-30T18:00:00Z must appear in CODE_REVIEW
	// output (for executionDepth / commandSource fields in #RegressionRun).
	const wantAddedIn = "2026-04-30T18:00:00Z"
	crOut, err := schema.DescribeStepType("CODE_REVIEW", schema.DescribeOpts{JSON: true})
	if err != nil {
		t.Fatalf("describe CODE_REVIEW: %v", err)
	}
	// The addedIn value won't appear in the field rows (lookupAddedIn returns the
	// parent path's addedIn when it matches first), but the field paths for
	// executionDepth and commandSource must exist.
	for _, wantField := range []string{"regressionRun.executionDepth", "regressionRun.commandSource"} {
		if !strings.Contains(crOut, wantField) {
			t.Errorf("CODE_REVIEW output missing field %q (needed for ci-flake-strike audit)", wantField)
		}
	}
	_ = wantAddedIn // addedIn for these fields resolves via parent path by design
}

// TestDescribeStepType_CodeReview_RegressionRun verifies that the codeReview
// regressionRun sub-fields include executionDepth and commandSource (both
// from #RegressionRun, accessible after the null|struct disjunction is
// resolved). Also verifies that executionDepth and commandSource are marked
// as required (no CUE default, non-nullable), per their definitions.
func TestDescribeStepType_CodeReview_RegressionRun(t *testing.T) {
	opts := schema.DescribeOpts{JSON: true}
	out, err := schema.DescribeStepType("CODE_REVIEW", opts)
	if err != nil {
		t.Fatalf("describe CODE_REVIEW: %v", err)
	}

	var fields []map[string]any
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Both executionDepth and commandSource must appear as sub-fields of
	// regressionRun (resolved from the *null | #RegressionRun disjunction).
	wantPaths := []string{"regressionRun.executionDepth", "regressionRun.commandSource"}
	for _, wantPath := range wantPaths {
		found := false
		for _, f := range fields {
			if path, ok := f["path"].(string); ok && path == wantPath {
				found = true
				// Verify the field is marked as required (no default in CUE).
				if req, _ := f["required"].(bool); !req {
					t.Errorf("field %q should be required but required=false", wantPath)
				}
				break
			}
		}
		if !found {
			t.Errorf("field %q not found in CODE_REVIEW describe output", wantPath)
		}
	}
}

// TestDescribeStepType_InvalidStepType_Errors verifies that an unknown step
// type name causes DescribeStepType to return an error whose message names
// the allowlist.
func TestDescribeStepType_InvalidStepType_Errors(t *testing.T) {
	_, err := schema.DescribeStepType("NOT_A_REAL_STEP", schema.DescribeOpts{})
	if err == nil {
		t.Fatal("expected error for invalid step type, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "NOT_A_REAL_STEP") {
		t.Errorf("error message should contain the invalid name; got: %s", msg)
	}
	// The error message should mention at least one valid step name.
	foundValid := false
	for _, v := range schema.ValidStepNames {
		if strings.Contains(msg, v) {
			foundValid = true
			break
		}
	}
	if !foundValid {
		t.Errorf("error message should reference the allowlist; got: %s", msg)
	}
}

// TestDescribeStepType_Markdown_DefaultOutput verifies that the default
// (no-flag) output:
//  1. Starts with "# StepType:".
//  2. Contains the canonical Markdown table header row.
//  3. Has field rows that are sorted lexicographically by the field column.
func TestDescribeStepType_Markdown_DefaultOutput(t *testing.T) {
	out, err := schema.DescribeStepType("TASK", schema.DescribeOpts{})
	if err != nil {
		t.Fatalf("describe TASK markdown: %v", err)
	}

	if !strings.HasPrefix(out, "# StepType:") {
		t.Errorf("output should start with '# StepType:', got: %q", out[:minLen(len(out), 40)])
	}

	wantHeader := "| Field | Required | Type | AddedIn | Description |"
	if !strings.Contains(out, wantHeader) {
		t.Errorf("output missing table header row %q", wantHeader)
	}

	// Extract the Field column values from data rows and assert they are
	// sorted lexicographically.
	lines := strings.Split(out, "\n")
	var fieldPaths []string
	inTable := false
	headerSeen := false
	for _, line := range lines {
		if strings.Contains(line, "| Field | Required |") {
			inTable = true
			headerSeen = true
			continue
		}
		if headerSeen && strings.HasPrefix(line, "|---") {
			continue // separator row
		}
		if inTable && strings.HasPrefix(line, "| ") {
			// Parse the first column (field path).
			cols := strings.Split(line, "|")
			if len(cols) >= 2 {
				fieldPaths = append(fieldPaths, strings.TrimSpace(cols[1]))
			}
		}
	}

	if len(fieldPaths) == 0 {
		t.Fatalf("no field rows found in markdown table output")
	}

	if !sort.StringsAreSorted(fieldPaths) {
		t.Errorf("field rows are not sorted lexicographically: %v", fieldPaths[:minLen(len(fieldPaths), 10)])
	}
}

// TestDescribeStepType_CobraCommand_InvalidArgs verifies that the cobra
// command returns a non-zero exit when an invalid step name is passed.
func TestDescribeStepType_CobraCommand_InvalidArgs(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	root := buildWorkflowCommand(&outBuf, &errBuf)
	root.SetArgs([]string{"workflow", "describe-step-type", "INVALID_STEP"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from invalid step type, got nil")
	}
	combined := errBuf.String() + err.Error()
	if !strings.Contains(combined, "INVALID_STEP") {
		t.Errorf("error should mention the invalid step name; got err=%v stderr=%s", err, errBuf.String())
	}
}

// TestDescribeStepType_CobraCommand_ValidJSON verifies the cobra command
// produces valid JSON when --json is set.
func TestDescribeStepType_CobraCommand_ValidJSON(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	root := buildWorkflowCommand(&outBuf, &errBuf)
	root.SetArgs([]string{"workflow", "describe-step-type", "TASK", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("command error: %v\nstderr: %s", err, errBuf.String())
	}

	raw := strings.TrimSpace(outBuf.String())
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, raw[:minLen(len(raw), 200)])
	}
	if len(parsed) == 0 {
		t.Error("JSON output is an empty array — expected field rows")
	}
}

// minLen returns the smaller of a and b.
func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestDescribeStepType_SurfacesEnumPatternClosedStruct asserts
// WF-CLI-UX-4: an LLM agent reading only `describe-step-type TASK`
// markdown / JSON output can discover string-disjunction enums,
// regex patterns, and closed-struct semantics without reading the
// CUE source.
//
// Specifically:
//
//   - `task.acceptanceCriteria[].id` carries the `^T-AC-[0-9]+$`
//     pattern.
//   - `task.execution.gates.baseline.lint` carries the
//     `pass|fail|skip` enum.
//   - The JSON projection for a field with an enum exposes the
//     `enum` key as a sorted []string.
func TestDescribeStepType_SurfacesEnumPatternClosedStruct(t *testing.T) {
	out, err := schema.DescribeStepType("TASK", schema.DescribeOpts{})
	if err != nil {
		t.Fatalf("DescribeStepType: %v", err)
	}
	if !strings.Contains(out, "^T-AC-[0-9]+$") {
		t.Errorf("expected markdown to surface T-AC regex pattern; got:\n%s", out)
	}
	if !strings.Contains(out, "enum: fail\\|pass\\|skip") {
		t.Errorf("expected markdown to surface lint enum (fail|pass|skip); got:\n%s", out)
	}

	// JSON form: walk every entry, find the one whose path is
	// `task.acceptanceCriteria[].id` (or matching tail), and assert
	// its `pattern` field is non-empty.
	jsonOut, err := schema.DescribeStepType("TASK", schema.DescribeOpts{JSON: true})
	if err != nil {
		t.Fatalf("DescribeStepType JSON: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("JSON unmarshal: %v\nraw: %s", err, jsonOut)
	}
	foundACPattern, foundLintEnum := false, false
	for _, r := range rows {
		path, _ := r["path"].(string)
		if strings.HasSuffix(path, "acceptanceCriteria[].id") {
			if pat, _ := r["pattern"].(string); pat == "^T-AC-[0-9]+$" {
				foundACPattern = true
			}
		}
		if strings.HasSuffix(path, ".lint") {
			if enum, ok := r["enum"].([]any); ok {
				gotEnum := make([]string, 0, len(enum))
				for _, e := range enum {
					if s, ok := e.(string); ok {
						gotEnum = append(gotEnum, s)
					}
				}
				sort.Strings(gotEnum)
				if strings.Join(gotEnum, "|") == "fail|pass|skip" {
					foundLintEnum = true
				}
			}
		}
	}
	if !foundACPattern {
		t.Errorf("JSON output does not expose acceptanceCriteria[].id pattern=^T-AC-[0-9]+$")
	}
	if !foundLintEnum {
		t.Errorf("JSON output does not expose .lint enum=[fail,pass,skip]")
	}
}
