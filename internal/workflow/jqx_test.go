package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestApplyJQWithVars_StringBindInjectsLiteral verifies that --arg-style
// bindings produce literal jq variables (no shell quoting needed).
func TestApplyJQWithVars_StringBindInjectsLiteral(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"steps":[{"stepId":"S1"},{"stepId":"S2"}]}`), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := ApplyJQWithVars(doc,
		`(.steps[] | select(.stepId == $id)) |= (. + {hit: true}) | .`,
		map[string]any{"id": "S2"},
	)
	if err != nil {
		t.Fatalf("ApplyJQWithVars: %v", err)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"hit":true`) {
		t.Fatalf("expected hit:true in output, got %s", b)
	}
}

// TestApplyJQWithVars_JSONBindAcceptsArray verifies that --argjson-style
// bindings carry arbitrary JSON values into the expression.
func TestApplyJQWithVars_JSONBindAcceptsArray(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"items":[]}`), &doc); err != nil {
		t.Fatal(err)
	}
	var changes any
	if err := json.Unmarshal([]byte(`[{"action":"added","file":"a.go"}]`), &changes); err != nil {
		t.Fatal(err)
	}
	got, err := ApplyJQWithVars(doc,
		`.items = $changes`,
		map[string]any{"changes": changes},
	)
	if err != nil {
		t.Fatalf("ApplyJQWithVars: %v", err)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"file":"a.go"`) {
		t.Fatalf("expected items to carry binding, got %s", b)
	}
}

// TestApplyJQWithVars_RejectsInvalidVarName verifies that a bind name
// failing the [A-Za-z_][A-Za-z0-9_]* rule is rejected before compilation.
func TestApplyJQWithVars_RejectsInvalidVarName(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{}`), &doc)
	cases := []string{"", "1id", "id-with-dash", "id with space", "id$"}
	for _, name := range cases {
		_, err := ApplyJQWithVars(doc, `.x = 1`, map[string]any{name: "v"})
		if err == nil {
			t.Errorf("expected error for var name %q, got nil", name)
		}
	}
}

// TestApplyJQWithVars_AcceptsValidIdentifiers pins the positive arm of the
// identifier check (QA-1). Without this, a future tightening of
// isValidJQVarName (e.g. forbidding leading underscore) would silently pass.
// Each case must succeed AND its bind value must surface in the result.
func TestApplyJQWithVars_AcceptsValidIdentifiers(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{}`), &doc)
	cases := []string{"x", "_x", "X", "a1", "_", "snake_case", "CamelCase", "_2"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := ApplyJQWithVars(doc, `{out: $`+name+`}`, map[string]any{name: "ok"})
			if err != nil {
				t.Fatalf("expected ApplyJQWithVars to accept %q, got error: %v", name, err)
			}
			m, ok := result.(map[string]any)
			if !ok {
				t.Fatalf("expected map result for %q, got %T: %v", name, result, result)
			}
			if m["out"] != "ok" {
				t.Errorf("expected bind value 'ok' to surface for %q, got %v", name, m["out"])
			}
		})
	}
}

// TestApplyJQWithVars_RejectsUnicodeIdentifiers pins the ASCII-only invariant
// (QA-2). The current implementation uses regexp ^[A-Za-z_][A-Za-z0-9_]*$
// which rejects any non-ASCII byte. A future refactor to unicode.IsLetter
// would broaden the contract beyond the documented regex; this test catches
// that drift.
func TestApplyJQWithVars_RejectsUnicodeIdentifiers(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{}`), &doc)
	cases := []string{"café", "🚀", "naïve", "αβγ", "ümlaut"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ApplyJQWithVars(doc, `.x = 1`, map[string]any{name: "v"})
			if err == nil {
				t.Errorf("expected non-ASCII identifier %q to be rejected, got nil error", name)
			}
		})
	}
}

// TestApplyJQ_BackcompatShimMatchesWithVarsNoVars closes SA-9: the public
// ApplyJQ(data, expr) shim must be byte-equal to ApplyJQWithVars(data, expr,
// nil). Without this, a subtle gojq-version regression in the shim wrapper
// would slip past the existing TestApplyJQ_EnvBuiltinReturnsEmpty.
func TestApplyJQ_BackcompatShimMatchesWithVarsNoVars(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{
	  "config": {"mode": "autonomous"},
	  "steps": [{"stepId": "S1", "name": "TASK", "task": {"title": "x"}}],
	  "totalSteps": 1
	}`), &doc); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		`.steps[0].stepId`,
		`(.steps | length)`,
		`.steps |= map(. + {hit: true})`,
		`{a: .config.mode, b: (.steps | length)}`,
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			shimResult, shimErr := ApplyJQ(doc, expr)
			fullResult, fullErr := ApplyJQWithVars(doc, expr, nil)
			if (shimErr == nil) != (fullErr == nil) {
				t.Fatalf("shim/full err mismatch: shim=%v full=%v", shimErr, fullErr)
			}
			shimBytes, _ := json.Marshal(shimResult)
			fullBytes, _ := json.Marshal(fullResult)
			if string(shimBytes) != string(fullBytes) {
				t.Errorf("shim != full for %q\n  shim: %s\n  full: %s", expr, shimBytes, fullBytes)
			}
		})
	}
}

// TestApplyJQ_EnvBuiltinReturnsEmpty verifies that the gojq `env` builtin
// returns an empty object (not the process environment) so that secrets
// cannot be exfiltrated into workflow.json via jq expressions.
// Covers F-sec-4 (OWASP A09 — sensitive data in logs / version-controlled files).
func TestApplyJQ_EnvBuiltinReturnsEmpty(t *testing.T) {
	t.Setenv("CR_TEST_SECRET", "leak-this")

	var doc any
	if err := json.Unmarshal([]byte(`{}`), &doc); err != nil {
		t.Fatal(err)
	}

	result, err := ApplyJQ(doc, "env")
	if err != nil {
		t.Fatalf("ApplyJQ(env) unexpected error: %v", err)
	}

	// Encode result to check for the secret value.
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal env result: %v", err)
	}
	output := string(b)

	if strings.Contains(output, "leak-this") {
		t.Errorf("env builtin leaked secret into jq output: %s", output)
	}
}

// TestApplyJQ_EnvKeyAccessReturnsNull verifies that accessing a specific env
// key via `env.CR_TEST_SECRET` does not return the real value.
// Covers F-sec-4 (OWASP A09 — env var exfiltration prevention).
func TestApplyJQ_EnvKeyAccessReturnsNull(t *testing.T) {
	t.Setenv("CR_TEST_SECRET", "leak-this")

	var doc any
	if err := json.Unmarshal([]byte(`{}`), &doc); err != nil {
		t.Fatal(err)
	}

	result, err := ApplyJQ(doc, "env.CR_TEST_SECRET")
	if err != nil {
		// An error is also acceptable — it means the builtin is disabled.
		return
	}

	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal env.CR_TEST_SECRET result: %v", err)
	}
	output := string(b)

	if strings.Contains(output, "leak-this") {
		t.Errorf("env.CR_TEST_SECRET leaked secret into jq output: %s", output)
	}
}

// testDoc is the JSON document used for all jqx tests.
const testDoc = `{
  "schemaVersion": 1,
  "featureId": "feat-test",
  "config": {
    "mode": "autonomous",
    "setAt": "2026-04-29T00:00:00Z"
  },
  "totalSteps": 3,
  "steps": [
    {
      "stepId": "STEP_01",
      "name": "BRAINSTORMING",
      "status": "COMPLETED"
    }
  ]
}`

// TestGetField_ScalarUnquoted verifies that GetField returns scalar string
// values unquoted (raw, not JSON-encoded).
// Covers T1-T-8: GetField returns scalars unquoted.
func TestGetField_ScalarUnquoted(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(testDoc), &doc); err != nil {
		t.Fatal(err)
	}

	got, err := GetField(doc, "featureId", false)
	if err != nil {
		t.Fatalf("GetField featureId: %v", err)
	}
	// Scalar string — must NOT be quoted.
	if got != "feat-test" {
		t.Errorf("expected raw string %q, got %q", "feat-test", got)
	}
	if strings.HasPrefix(got, `"`) {
		t.Errorf("scalar should not be quoted, got %q", got)
	}
}

// TestGetField_NumberScalarUnquoted verifies that numeric scalars are returned
// as plain number strings, not JSON.
// Covers T1-T-8: GetField returns scalars unquoted.
func TestGetField_NumberScalarUnquoted(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(testDoc), &doc); err != nil {
		t.Fatal(err)
	}

	got, err := GetField(doc, "schemaVersion", false)
	if err != nil {
		t.Fatalf("GetField schemaVersion: %v", err)
	}
	if got != "1" {
		t.Errorf("expected %q, got %q", "1", got)
	}
}

// TestGetField_ObjectAsJSON verifies that when the field resolves to an object,
// GetField returns it as JSON.
// Covers T1-T-8: objects/arrays returned as JSON.
func TestGetField_ObjectAsJSON(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(testDoc), &doc); err != nil {
		t.Fatal(err)
	}

	got, err := GetField(doc, "config", false)
	if err != nil {
		t.Fatalf("GetField config: %v", err)
	}
	// Must be valid JSON object.
	var parsed any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Errorf("expected valid JSON for object field, got %q: %v", got, err)
	}
	if !strings.Contains(got, "mode") {
		t.Errorf("expected JSON to contain 'mode', got %q", got)
	}
}

// TestGetField_ArrayAsJSON verifies that when the field resolves to an array,
// GetField returns it as JSON.
// Covers T1-T-8: objects/arrays returned as JSON.
func TestGetField_ArrayAsJSON(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(testDoc), &doc); err != nil {
		t.Fatal(err)
	}

	got, err := GetField(doc, "steps", false)
	if err != nil {
		t.Fatalf("GetField steps: %v", err)
	}
	if !strings.HasPrefix(got, "[") {
		t.Errorf("expected JSON array, got %q", got)
	}
}

// TestGetField_NestedDotPath verifies that dot-separated paths like
// "config.mode" resolve correctly.
// Covers T1-T-8: dotted path resolution.
func TestGetField_NestedDotPath(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(testDoc), &doc); err != nil {
		t.Fatal(err)
	}

	got, err := GetField(doc, "config.mode", false)
	if err != nil {
		t.Fatalf("GetField config.mode: %v", err)
	}
	if got != "autonomous" {
		t.Errorf("expected %q, got %q", "autonomous", got)
	}
}

// TestGetField_MissingPathReturnsError verifies that an unknown/missing path
// returns an error rather than silently returning empty.
// Covers T1-T-8: missing path returns error.
func TestGetField_MissingPathReturnsError(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(testDoc), &doc); err != nil {
		t.Fatal(err)
	}

	_, err := GetField(doc, "nonexistent.deep.path", false)
	if err == nil {
		t.Error("expected error for missing path, got nil")
	}
}

// ---- rewriteMultiStatementJQ tests ----------------------------------------

// TestRewriteMultiStatement_BasicSplit verifies that a two-statement program
// separated by a top-level `;` is rewritten to a `(stmt1) | (stmt2)` pipeline
// and that running it through ApplyJQ applies both mutations.
func TestRewriteMultiStatement_BasicSplit(t *testing.T) {
	// Pure rewrite check.
	got := rewriteMultiStatementJQ(`.a = 1 ; .b = 2`)
	want := "(.a = 1\n) | (.b = 2\n)"
	if got != want {
		t.Errorf("rewrite: got %q, want %q", got, want)
	}

	// End-to-end via ApplyJQ: both mutations must land.
	var doc any
	if err := json.Unmarshal([]byte(`{}`), &doc); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyJQ(doc, `.a = 1 ; .b = 2`)
	if err != nil {
		t.Fatalf("ApplyJQ multi-stmt: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	// gojq returns numbers as float64 or int; accept both.
	aVal := fmt.Sprintf("%v", m["a"])
	bVal := fmt.Sprintf("%v", m["b"])
	if aVal != "1" {
		t.Errorf("expected .a=1, got %v", m["a"])
	}
	if bVal != "2" {
		t.Errorf("expected .b=2, got %v", m["b"])
	}
}

// TestRewriteMultiStatement_NestedSemicolonsPreserved verifies that `;` inside
// strings, parentheses, or brackets is NOT treated as a statement separator.
func TestRewriteMultiStatement_NestedSemicolonsPreserved(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		// String literal: the `;` is inside `"a;b"` — must not split.
		{name: "string-literal", input: `.x = "a;b"`},
		// Inside parens: gojq's optional-output `(.x; .y)` — must not split at top level.
		{name: "inside-parens", input: `(.x; .y) | .z`},
		// Inside brackets: gojq array constructor `[.a; .b]` — must not split.
		{name: "inside-brackets", input: `[.a; .b]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteMultiStatementJQ(tc.input)
			if got != tc.input {
				t.Errorf("expected verbatim %q, got %q", tc.input, got)
			}
		})
	}

	// Mix: only the top-level `;` between `"x;y"` and `.bar` splits.
	mix := `.foo = "x;y" ; .bar = 1`
	gotMix := rewriteMultiStatementJQ(mix)
	wantMix := "(.foo = \"x;y\"\n) | (.bar = 1\n)"
	if gotMix != wantMix {
		t.Errorf("mix rewrite: got %q, want %q", gotMix, wantMix)
	}
}

// TestRewriteMultiStatement_SingleStatementUnchanged verifies that a program
// with no top-level `;` is returned byte-equal to input (no parens added).
func TestRewriteMultiStatement_SingleStatementUnchanged(t *testing.T) {
	cases := []string{
		`.foo = 1`,
		`(.steps[] | select(.stepId == "S1")) |= (. + {hit: true}) | .`,
		`{a: 1, b: 2}`,
		``,
	}
	for _, c := range cases {
		got := rewriteMultiStatementJQ(c)
		if got != c {
			t.Errorf("input %q: expected verbatim, got %q", c, got)
		}
	}
}

// TestRewriteMultiStatement_DefStatementsRespected verifies that programs
// containing a top-level `def ` keyword are returned verbatim. Splitting
// across a def body's `;` would produce two fragments gojq cannot compile.
// Decision: bail out on any top-level `def ` and let gojq handle natively.
func TestRewriteMultiStatement_DefStatementsRespected(t *testing.T) {
	prog := `def square: . * .; .x | square`
	got := rewriteMultiStatementJQ(prog)
	if got != prog {
		t.Errorf("def program: expected verbatim, got %q", got)
	}

	// Verify it actually executes correctly through ApplyJQ (gojq handles def fine).
	var doc any
	if err := json.Unmarshal([]byte(`{"x": 3}`), &doc); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyJQ(doc, prog)
	if err != nil {
		t.Fatalf("ApplyJQ def: %v", err)
	}
	val := fmt.Sprintf("%v", result)
	if val != "9" {
		t.Errorf("expected 9, got %v", result)
	}
}

// TestRewriteMultiStatement_TrailingSemicolon verifies that a trailing `;`
// is treated as an empty last statement (skipped), leaving a single-statement
// result that is returned verbatim.
func TestRewriteMultiStatement_TrailingSemicolon(t *testing.T) {
	got := rewriteMultiStatementJQ(`.a = 1 ;`)
	// Only one non-empty statement → single-statement path → verbatim.
	if got != `.a = 1 ;` {
		t.Errorf("trailing semicolon: got %q, want verbatim", got)
	}
}

// TestRewriteMultiStatement_EmptyStatement verifies defensive handling of
// empty/blank statements (e.g. `;;` or `; .a = 1`). Empty tokens are skipped;
// the remaining non-empty statement(s) drive the outcome.
func TestRewriteMultiStatement_EmptyStatement(t *testing.T) {
	cases := []struct {
		input string
		// expectMulti is true when we expect a pipeline rewrite (≥2 non-empty stmts).
		expectMulti bool
	}{
		// Two consecutive semicolons → only one empty token + one empty token → zero stmts → verbatim.
		{input: `;;`, expectMulti: false},
		// Leading semicolon + one real statement → single non-empty stmt → verbatim.
		{input: `; .a = 1`, expectMulti: false},
		// Two real statements separated by `;` even with whitespace → rewrite.
		{input: `.a = 1 ; .b = 2`, expectMulti: true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := rewriteMultiStatementJQ(tc.input)
			isPipeline := strings.Contains(got, " | ")
			if isPipeline != tc.expectMulti {
				t.Errorf("input %q: expectMulti=%v but got %q", tc.input, tc.expectMulti, got)
			}
		})
	}
}

// TestApplyJQ_MultiStatementEndToEnd invokes ApplyJQ and ApplyJQWithVars
// directly with a multi-statement program and asserts all mutations applied.
func TestApplyJQ_MultiStatementEndToEnd(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"featureName":"old","totalElapsedMin":0}`), &doc); err != nil {
		t.Fatal(err)
	}

	// Three-statement program mimicking a real skill invocation.
	expr := `.featureName = "x" ; .totalElapsedMin = 1`
	result, err := ApplyJQ(doc, expr)
	if err != nil {
		t.Fatalf("ApplyJQ multi-stmt: %v", err)
	}
	b, _ := json.Marshal(result)
	s := string(b)
	if !strings.Contains(s, `"featureName":"x"`) {
		t.Errorf("expected featureName=x, got %s", s)
	}
	if !strings.Contains(s, `"totalElapsedMin":1`) {
		t.Errorf("expected totalElapsedMin=1, got %s", s)
	}

	// Also test with ApplyJQWithVars + bind variable.
	var doc2 any
	if err := json.Unmarshal([]byte(`{"name":"","count":0}`), &doc2); err != nil {
		t.Fatal(err)
	}
	result2, err := ApplyJQWithVars(doc2, `.name = $n ; .count = 42`, map[string]any{"n": "hello"})
	if err != nil {
		t.Fatalf("ApplyJQWithVars multi-stmt: %v", err)
	}
	b2, _ := json.Marshal(result2)
	s2 := string(b2)
	if !strings.Contains(s2, `"name":"hello"`) {
		t.Errorf("expected name=hello, got %s", s2)
	}
	if !strings.Contains(s2, `"count":42`) {
		t.Errorf("expected count=42, got %s", s2)
	}
}

// TestRewriteMultiStatement_CommentSemicolonsPreserved verifies that a `;`
// inside a line comment is NOT treated as a statement separator. Only the
// real top-level `;` after the comment ends drives the split. (F-23)
func TestRewriteMultiStatement_CommentSemicolonsPreserved(t *testing.T) {
	input := ".x = 1 # comment with ; here\n; .y = 2"

	// End-to-end: run through ApplyJQ to verify both keys are mutated.
	// Asserting on execution outcome is more robust than string-shape checks
	// since the rewriter may produce different whitespace arrangements.
	var doc any
	if err := json.Unmarshal([]byte(`{"x": 0, "y": 0}`), &doc); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyJQ(doc, input)
	if err != nil {
		t.Fatalf("ApplyJQ with comment semicolon: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	xVal := fmt.Sprintf("%v", m["x"])
	yVal := fmt.Sprintf("%v", m["y"])
	if xVal != "1" {
		t.Errorf("expected .x=1 after split, got %v", m["x"])
	}
	if yVal != "2" {
		t.Errorf("expected .y=2 after split, got %v", m["y"])
	}
}

// TestRewriteMultiStatement_ThreeStatements verifies that a three-statement
// program applies all three mutations. This catches mutations like
// `len(stmts) > 1` → `len(stmts) > 2` that survive 2-statement tests. (F-29)
func TestRewriteMultiStatement_ThreeStatements(t *testing.T) {
	input := `.a = 1 ; .b = 2 ; .c = 3`

	var doc any
	if err := json.Unmarshal([]byte(`{"a": 0, "b": 0, "c": 0}`), &doc); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyJQ(doc, input)
	if err != nil {
		t.Fatalf("ApplyJQ three-statement: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	aVal := fmt.Sprintf("%v", m["a"])
	bVal := fmt.Sprintf("%v", m["b"])
	cVal := fmt.Sprintf("%v", m["c"])
	if aVal != "1" {
		t.Errorf("expected .a=1, got %v", m["a"])
	}
	if bVal != "2" {
		t.Errorf("expected .b=2, got %v", m["b"])
	}
	if cVal != "3" {
		t.Errorf("expected .c=3, got %v", m["c"])
	}
}

// TestGetField_JSONModeScalarWrapped verifies that when jsonMode=true, a
// scalar string is returned as a JSON string (quoted), and a scalar number is
// returned as a JSON literal.
// Covers T1-T-8: --json mode forces JSON formatting on scalars.
func TestGetField_JSONModeScalarWrapped(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(testDoc), &doc); err != nil {
		t.Fatal(err)
	}

	t.Run("string-in-json-mode", func(t *testing.T) {
		got, err := GetField(doc, "featureId", true)
		if err != nil {
			t.Fatalf("GetField featureId json mode: %v", err)
		}
		// Must be JSON-encoded string: "feat-test"
		if got != `"feat-test"` {
			t.Errorf("expected JSON string %q, got %q", `"feat-test"`, got)
		}
	})

	t.Run("number-in-json-mode", func(t *testing.T) {
		got, err := GetField(doc, "schemaVersion", true)
		if err != nil {
			t.Fatalf("GetField schemaVersion json mode: %v", err)
		}
		// Must be valid JSON literal (number as JSON).
		var n any
		if err := json.Unmarshal([]byte(got), &n); err != nil {
			t.Errorf("expected JSON literal for number, got %q: %v", got, err)
		}
	})
}
