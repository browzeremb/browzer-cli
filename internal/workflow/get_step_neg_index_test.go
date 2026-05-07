package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// arrayDoc is the JSON document used for path-with-array-index tests.
// Mirrors a realistic workflow fragment: task.execution.agents[].
const arrayDoc = `{
  "task": {
    "execution": {
      "agents": [
        {"id": "agent-1", "role": "explorer", "notes": "first"},
        {"id": "agent-2", "role": "reviewer", "notes": "middle"},
        {"id": "agent-3", "role": "implementer", "notes": "last"}
      ]
    }
  },
  "scope": ["a.go", "b.go"]
}`

// TestGetField_PositiveIndex verifies that bracket-indexed positive lookups
// return the addressed element. Pins existing behaviour so the negative-index
// patch does not regress positive paths.
func TestGetField_PositiveIndex(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := GetField(doc, "task.execution.agents[0].id", false)
	if err != nil {
		t.Fatalf("GetField agents[0].id: %v", err)
	}
	if got != "agent-1" {
		t.Errorf("expected agent-1, got %q", got)
	}
}

// TestGetField_NegativeIndexLast verifies that [-1] returns the last array
// element — the canonical use case from RETRO §16 #3 and AC-4.
func TestGetField_NegativeIndexLast(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := GetField(doc, "task.execution.agents[-1]", false)
	if err != nil {
		t.Fatalf("GetField agents[-1]: %v", err)
	}
	if !strings.Contains(got, `"id":"agent-3"`) {
		t.Errorf("expected agent-3 object, got %q", got)
	}
}

// TestGetField_NegativeIndexLastChained verifies `[-1].notes` returns the
// scalar field of the last array element.
func TestGetField_NegativeIndexLastChained(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := GetField(doc, "task.execution.agents[-1].notes", false)
	if err != nil {
		t.Fatalf("GetField agents[-1].notes: %v", err)
	}
	if got != "last" {
		t.Errorf("expected 'last', got %q", got)
	}
}

// TestGetField_NegativeIndexSecondToLast pins [-2] -> second-to-last (AC-4).
func TestGetField_NegativeIndexSecondToLast(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := GetField(doc, "task.execution.agents[-2].id", false)
	if err != nil {
		t.Fatalf("GetField agents[-2].id: %v", err)
	}
	if got != "agent-2" {
		t.Errorf("expected agent-2, got %q", got)
	}
}

// TestGetField_NegativeIndexOutOfRange verifies that an over-deep negative
// index returns an error consistent with positive out-of-range — never panics
// (AC-5 / T-3).
func TestGetField_NegativeIndexOutOfRange(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	_, err := GetField(doc, "task.execution.agents[-99]", false)
	if err == nil {
		t.Error("expected error for out-of-range negative index, got nil")
	}
}

// TestGetField_PositiveIndexOutOfRange parity-pins the positive OOB arm so
// AC-5 (positive and negative behave the same) is symmetric.
func TestGetField_PositiveIndexOutOfRange(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	_, err := GetField(doc, "task.execution.agents[99]", false)
	if err == nil {
		t.Error("expected error for out-of-range positive index, got nil")
	}
}

// TestGetField_IndexOnMissingPath verifies that an index applied to a missing
// key still produces a structured error (not a panic).
func TestGetField_IndexOnMissingPath(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	_, err := GetField(doc, "task.execution.nonexistent[-1]", false)
	if err == nil {
		t.Error("expected error for missing path with index, got nil")
	}
}

// TestGetField_TopLevelArrayIndex verifies that bracket indexing works on a
// top-level array path ("scope[-1]") without an intermediate object hop.
func TestGetField_TopLevelArrayIndex(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(arrayDoc), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := GetField(doc, "scope[-1]", false)
	if err != nil {
		t.Fatalf("GetField scope[-1]: %v", err)
	}
	if got != "b.go" {
		t.Errorf("expected b.go, got %q", got)
	}
}

// TestParseFieldPath_Malformed pins the error-arm of the path parser so
// future drift (e.g. silently accepting "[abc]") fails loudly.
func TestParseFieldPath_Malformed(t *testing.T) {
	cases := []string{
		"foo[",       // unclosed bracket
		"foo[]",      // empty index
		"foo[abc]",   // non-integer index
		"foo]",       // stray ']'
		"",           // empty path
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			_, err := parseFieldPath(p)
			if err == nil {
				t.Errorf("expected parse error for %q, got nil", p)
			}
		})
	}
}

// F-9 edge cases — pin the null-handling and empty-array behaviour of
// GetField so future refactors cannot silently drift.
//
// (a) JSON null SCALAR returns the bare string "null" (not an error).
// (b) Negative-index lookup INTO a null array yields a structured error
//     mentioning "expected array".
// (c) [-1] on a length-0 array is an out-of-range error, not "null".

func TestGetField_NullScalar_ReturnsNullString(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"foo": null}`), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := GetField(doc, "foo", false)
	if err != nil {
		t.Fatalf("GetField foo: %v", err)
	}
	if got != "null" {
		t.Errorf("expected 'null' for JSON null scalar, got %q", got)
	}
}

func TestGetField_NegIndexOnNullIntermediate_Errors(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"agents": null}`), &doc); err != nil {
		t.Fatal(err)
	}
	_, err := GetField(doc, "agents[-1]", false)
	if err == nil {
		t.Fatalf("expected error indexing into null, got nil")
	}
	if !strings.Contains(err.Error(), "array") {
		t.Errorf("expected 'array' in error message, got %q", err.Error())
	}
}

func TestGetField_NegIndexOnEmptyArray_Errors(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"agents": []}`), &doc); err != nil {
		t.Fatal(err)
	}
	_, err := GetField(doc, "agents[-1]", false)
	if err == nil {
		t.Fatalf("expected out-of-range error on [-1] of empty array, got nil")
	}
}
