package schema

// TASK_01 — WRITE_TESTS phase schema completeness tests.
// Verifies the top-level mutationScore/killed/survived/categories aliases
// validate independently of (and alongside) the existing nested
// mutationTesting.{...} shape, preserving backward compatibility.

import (
	"path/filepath"
	"testing"
)

func TestWriteTests_OldNestedShape_Validates(t *testing.T) {
	td := writeTestsTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "old.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("write_tests/old.json must validate (backward compat)")
	}
}

func TestWriteTests_NewFlatShape_Validates(t *testing.T) {
	td := writeTestsTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "new.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("write_tests/new.json (flat top-level aliases) must validate")
	}
}

func TestWriteTests_MixedShape_Validates(t *testing.T) {
	td := writeTestsTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "mixed.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("write_tests/mixed.json must validate (flat + nested coexist)")
	}
}

func TestWriteTests_InvalidShape_Rejected(t *testing.T) {
	td := writeTestsTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "invalid.json")))
	if res.Valid {
		t.Fatalf("write_tests/invalid.json must be rejected")
	}
}

func writeTestsTestdata(t *testing.T) string {
	t.Helper()
	cwd := mustGetwd(t)
	cur := cwd
	for range 8 {
		c := filepath.Join(cur, "internal", "schema", "testdata", "write_tests")
		if isDir(c) {
			return c
		}
		c2 := filepath.Join(cur, "testdata", "write_tests")
		if isDir(c2) {
			return c2
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	t.Fatalf("could not locate testdata/write_tests from %s", cwd)
	return ""
}
