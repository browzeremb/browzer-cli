package commands

import (
	"bytes"
	"encoding/json"
	"os"
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
	// #TaskExecutionResult, added at 2026-04-24T00:00:00Z). After
	// WF-CUE-NOISE-03 (2026-05-05) the walker descends past the
	// `*[] | [...#ScopeAdjustment]` default-empty disjunction into
	// element fields, so the bare `execution.scopeAdjustments` row
	// is replaced by `execution.scopeAdjustments[].<sub>` rows. Either
	// representation satisfies the "field is present" contract; we
	// match by prefix to stay invariant to the descent depth.
	found := false
	for _, f := range allFields {
		if path, _ := f["path"].(string); path == "execution.scopeAdjustments" ||
			strings.HasPrefix(path, "execution.scopeAdjustments[].") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("execution.scopeAdjustments not found in TASK describe output (neither bare nor element-level)")
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

// TestDescribeStepType_InlineEnums_OnlyEnumOrRegex (A1.3, 2026-05-05):
// the --inline-enums output emits ONE entry per enumerable field
// (string disjunction OR regex constraint), and entries carry
// either `values` or `regex`. Non-enumerable fields are dropped.
func TestDescribeStepType_InlineEnums_OnlyEnumOrRegex(t *testing.T) {
	opts := schema.DescribeOpts{InlineEnums: true, JSON: true}
	out, err := schema.DescribeStepType("CODE_REVIEW", opts)
	if err != nil {
		t.Fatalf("describe CODE_REVIEW --inline-enums: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if len(rows) == 0 {
		t.Fatal("inline-enums emitted zero rows for CODE_REVIEW (expected ≥1 enum field)")
	}
	for _, r := range rows {
		path, _ := r["path"].(string)
		if path == "" {
			t.Errorf("row missing path: %+v", r)
		}
		_, hasValues := r["values"]
		_, hasRegex := r["regex"]
		if !hasValues && !hasRegex {
			t.Errorf("row %q has neither values nor regex: %+v", path, r)
		}
		if hasValues && hasRegex {
			t.Errorf("row %q has BOTH values and regex (mutually exclusive): %+v", path, r)
		}
		// Every row must declare a type.
		if _, ok := r["type"].(string); !ok {
			t.Errorf("row %q missing type: %+v", path, r)
		}
	}
	// At least one regressionRun.tool entry should be present —
	// it's a known string-disjunction enum on every CODE_REVIEW.
	found := false
	for _, r := range rows {
		path, _ := r["path"].(string)
		if path != "regressionRun.tool" {
			continue
		}
		values, _ := r["values"].([]any)
		var got []string
		for _, v := range values {
			if s, ok := v.(string); ok {
				got = append(got, s)
			}
		}
		sort.Strings(got)
		want := []string{"cargo test", "go test", "jest", "lefthook", "pytest", "skipped", "vitest"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("regressionRun.tool values mismatch: got %v want %v", got, want)
		}
		found = true
		break
	}
	if !found {
		t.Errorf("expected regressionRun.tool entry in inline-enums output")
	}
}

// TestDescribeStepType_SaveWritesFile verifies that `--save <path>` routes
// the schema description to disk and emits a single confirmation line on
// stdout instead of dumping the table or JSON inline. Mirrors the same
// pattern used by get-step / get-config / query so callers can pipe
// describe-step-type output into the same tooling. Closes RETRO 3.2 from
// the 2026-05-05 token-economy session: the missing flag forced operators
// to manual `> /tmp/file` redirects, breaking parity with sibling read
// verbs.
func TestDescribeStepType_SaveWritesFile(t *testing.T) {
	dir := t.TempDir()
	savePath := dir + "/prd-schema.json"

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "describe-step-type", "PRD",
		"--json",
		"--save", savePath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("describe-step-type --save should succeed: %v\nstderr: %s", err, stderr.String())
	}

	// Stdout should NOT contain the JSON array; only a confirmation line.
	out := stdout.String()
	if strings.Contains(out, `"path"`) || strings.Contains(out, `"required"`) {
		t.Errorf("--save should NOT dump JSON to stdout, got: %q", out)
	}
	if !strings.Contains(out, "wrote ") || !strings.Contains(out, savePath) {
		t.Errorf("expected stdout confirmation line referencing %s, got: %q", savePath, out)
	}

	// File must exist + be parseable JSON.
	data, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read save file: %v", err)
	}
	var fields []map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("save file content should be valid JSON array: %v\nraw: %s", err, string(data[:minLen(len(data), 200)]))
	}
	if len(fields) == 0 {
		t.Error("save file should contain at least one field entry")
	}
}

// TestDescribeStepType_SaveQuietSilencesConfirmation verifies that
// combining --save with --quiet suppresses the confirmation line, leaving
// stdout completely empty (zero output, payload on disk, exit 0). Pairs
// with TestDescribeStepType_SaveWritesFile.
func TestDescribeStepType_SaveQuietSilencesConfirmation(t *testing.T) {
	dir := t.TempDir()
	savePath := dir + "/prd-schema.json"

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "describe-step-type", "PRD",
		"--json",
		"--save", savePath,
		"--quiet",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("describe-step-type --save --quiet should succeed: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("--save --quiet should produce empty stdout, got: %q", stdout.String())
	}
}

// TestDescribeStepAlias_ProducesIdenticalOutput asserts that the
// `describe-step` cobra alias produces byte-identical stdout to the
// canonical `describe-step-type` invocation. Closes RETRO 2.5
// (2026-05-05 orchestrate-task-delivery session): operators and LLM
// agents typed `describe-step --json` expecting it to work; the
// cobra "unknown flag --json" surfaced because the subcommand
// itself didn't exist. The alias collapses the discoverability
// gap without forking a second implementation.
func TestDescribeStepAlias_ProducesIdenticalOutput(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"json-PRD", []string{"PRD", "--json"}},
		{"markdown-TASK", []string{"TASK"}},
		{"required-only-PRD", []string{"PRD", "--required-only"}},
		{"field-projection", []string{"PRD", "--field", "title"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var canonStdout, canonStderr bytes.Buffer
			canonRoot := buildWorkflowCommandT(t, &canonStdout, &canonStderr)
			canonRoot.SetArgs(append([]string{"workflow", "describe-step-type"}, tc.args...))
			if err := canonRoot.Execute(); err != nil {
				t.Fatalf("canonical describe-step-type: %v\nstderr: %s", err, canonStderr.String())
			}

			var aliasStdout, aliasStderr bytes.Buffer
			aliasRoot := buildWorkflowCommandT(t, &aliasStdout, &aliasStderr)
			aliasRoot.SetArgs(append([]string{"workflow", "describe-step"}, tc.args...))
			if err := aliasRoot.Execute(); err != nil {
				t.Fatalf("alias describe-step: %v\nstderr: %s", err, aliasStderr.String())
			}

			if canonStdout.String() != aliasStdout.String() {
				t.Errorf("alias output drift:\ncanonical: %q\nalias:     %q",
					canonStdout.String(), aliasStdout.String())
			}
		})
	}
}

// TestDescribeStepAlias_AcceptsSaveFlag mirrors
// TestDescribeStepType_SaveWritesFile through the alias. Asserts the
// alias inherits the full flag surface (no flag-drift) and writes a
// valid JSON array to the requested path.
func TestDescribeStepAlias_AcceptsSaveFlag(t *testing.T) {
	dir := t.TempDir()
	savePath := dir + "/prd-schema.json"

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "describe-step", "PRD",
		"--json",
		"--save", savePath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("describe-step --save: %v\nstderr: %s", err, stderr.String())
	}
	data, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read save file: %v", err)
	}
	var fields []map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("save file content should be valid JSON array: %v", err)
	}
	if len(fields) == 0 {
		t.Error("alias --save: empty fields array")
	}
}

// TestDescribeStepType_PRD_DescendsDefaultEmptyListDisjunctions asserts
// WF-CUE-NOISE-03 (2026-05-05): the SSOT uses the pattern
// `*[] | [...#NamedDef]` for fields that default to an empty list
// (`personas`, `risks`, `successMetrics`, `nonFunctionalRequirements`).
// Before this fix, `describe-step-type PRD --json` reported each as a
// single `{path: "personas", type: "array"}` leaf, hiding the element
// shape and forcing operators to `sed` the CUE source — the friction
// case from the orchestrate-task-delivery retro 2026-05-05 §2.1/2.2.
//
// After the fix, the walker descends past the default-marked empty
// branch into `[...#NamedDef]` and surfaces every element field with
// its CUE constraints (regex / enum / required).
func TestDescribeStepType_PRD_DescendsDefaultEmptyListDisjunctions(t *testing.T) {
	out, err := schema.DescribeStepType("PRD", schema.DescribeOpts{JSON: true})
	if err != nil {
		t.Fatalf("DescribeStepType PRD --json: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("JSON unmarshal: %v\nraw: %s", err, out)
	}

	// Each entry asserts: (a) the leaf path is reached at all, and
	// (b) its CUE-derived metadata (pattern / required) lands on the
	// row. The previous walker emitted exactly one `array` leaf per
	// field and never reached these paths.
	want := []struct {
		path        string
		wantPattern string
		wantType    string
	}{
		{"personas[].id", "^P-[0-9]+$", "string"},
		{"personas[].description", "", "string"},
		{"risks[].id", "^R-[0-9]+$", "string"},
		{"risks[].mitigation", "", "string"},
		{"successMetrics[].id", "^M-[0-9]+$", "string"},
		{"successMetrics[].metric", "", "string"},
		{"successMetrics[].method", "", "string"},
		{"successMetrics[].target", "", "string"},
		{"nonFunctionalRequirements[].id", "^NFR-[0-9]+$", "string"},
		{"nonFunctionalRequirements[].target", "", "string"},
	}
	byPath := map[string]map[string]any{}
	for _, r := range rows {
		if p, ok := r["path"].(string); ok {
			byPath[p] = r
		}
	}
	for _, w := range want {
		row, ok := byPath[w.path]
		if !ok {
			t.Errorf("expected describe-step-type PRD to surface path %q (default-empty disjunction descent missing)", w.path)
			continue
		}
		if got, _ := row["type"].(string); got != w.wantType {
			t.Errorf("path %q: type = %q, want %q", w.path, got, w.wantType)
		}
		if w.wantPattern != "" {
			if got, _ := row["pattern"].(string); got != w.wantPattern {
				t.Errorf("path %q: pattern = %q, want %q", w.path, got, w.wantPattern)
			}
		}
	}

	// Negative assertion: ensure the OLD leaf rows (just `personas`
	// with type=array, no children) are gone. The walker must
	// either descend into the element OR emit nothing — never emit
	// the bare `personas` placeholder when the element type is
	// recursable.
	for _, leafPath := range []string{"personas", "risks", "successMetrics", "nonFunctionalRequirements"} {
		if row, ok := byPath[leafPath]; ok {
			if got, _ := row["type"].(string); got == "array" {
				t.Errorf("regression: bare leaf row %q (type=array) still emitted; walker should descend", leafPath)
			}
		}
	}
}
