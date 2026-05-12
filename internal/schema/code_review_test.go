package schema

// TASK_01 — CODE_REVIEW phase schema completeness tests.
// Verifies:
//   - old shape (no new fields) still validates (backward compat)
//   - new shape (sensitivePathGate, gate, exitCode, mergedFrom) validates
//   - invalid gate value "bogus" is rejected

import (
	"path/filepath"
	"testing"
)

// findTestdataDir resolves <repo>/packages/cli/internal/schema/testdata/
// from any working directory within the module tree (up to 8 levels).
func findTestdataDir(t *testing.T) string {
	t.Helper()
	// The testdata dir is co-located with the test files in internal/schema/.
	// os.Getwd() inside a `go test ./internal/schema/...` invocation returns
	// the package directory, so testdata/ is directly adjacent.
	import_path := filepath.Join("testdata")
	// Resolve to absolute using findFixturesDir's walk-up helper so the path
	// works regardless of how the test binary is invoked.
	cwd := mustGetwd(t)
	cur := cwd
	for range 8 {
		candidate := filepath.Join(cur, "internal", "schema", "testdata")
		if isDir(candidate) {
			return candidate
		}
		// Also check direct adjacency (already inside internal/schema/).
		candidate2 := filepath.Join(cur, "testdata")
		if isDir(candidate2) {
			return candidate2
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	// Last resort: use the relative path (works when cwd == package dir).
	return import_path
}

// TestCodeReview_OldShape_ValidatesClean asserts a CODE_REVIEW body WITHOUT
// the new 2026-05-11 fields still validates (backward compat invariant).
func TestCodeReview_OldShape_ValidatesClean(t *testing.T) {
	td := findTestdataDir(t)
	payload := mustReadFile(t, filepath.Join(td, "code_review", "old.json"))
	res := ValidateWorkflow(payload)
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("code_review/old.json should be valid (backward compat); got %d violations", len(res.Violations))
	}
}

// TestCodeReview_NewShape_ValidatesClean asserts a CODE_REVIEW body WITH
// sensitivePathGate, gate: "fail-on-high", exitCode: 0, and
// findings[i].mergedFrom: ["SR-3", "QA-1"] validates cleanly.
func TestCodeReview_NewShape_ValidatesClean(t *testing.T) {
	td := findTestdataDir(t)
	payload := mustReadFile(t, filepath.Join(td, "code_review", "new.json"))
	res := ValidateWorkflow(payload)
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("code_review/new.json should be valid; got %d violations", len(res.Violations))
	}
}

// TestCodeReview_MergedFromExpandedRegex_ValidatesClean verifies F-015:
// mergedFrom entries using 4-char lane prefixes (e.g. "SECU-1", "FAST-2")
// satisfy the ^[A-Z]{2,6}-[0-9]+$ regex and validate cleanly. This covers the
// regex expansion beyond the 2-char prefixes present in new.json.
func TestCodeReview_MergedFromExpandedRegex_ValidatesClean(t *testing.T) {
	td := findTestdataDir(t)
	payload := mustReadFile(t, filepath.Join(td, "code_review", "merged_from_expanded.json"))
	res := ValidateWorkflow(payload)
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("merged_from_expanded.json should be valid (SECU-1 / FAST-2 match ^[A-Z]{2,6}-[0-9]+$); got %d violations", len(res.Violations))
	}
}

// TestCodeReview_InvalidGate_IsRejected asserts that gate: "bogus" is rejected
// by CUE (only "fail-on-high" | "fail-on-medium-or-high" | "advisory-only" are valid).
func TestCodeReview_InvalidGate_IsRejected(t *testing.T) {
	td := findTestdataDir(t)
	payload := mustReadFile(t, filepath.Join(td, "code_review", "invalid.json"))
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatalf("code_review/invalid.json should be rejected (gate: \"bogus\"); got 0 violations")
	}
	if len(res.Violations) == 0 {
		t.Fatalf("expected ≥1 violation for code_review/invalid.json; got empty slice")
	}
}
