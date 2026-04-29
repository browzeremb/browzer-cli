package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

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
