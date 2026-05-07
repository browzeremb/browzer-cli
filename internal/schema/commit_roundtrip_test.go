package schema

// R-14 + R-15-schema: regression tests for COMMIT round-trip and
// granularityWarnings[] schema addition.
//
// These tests exercise:
//   - commit-min.json validates with zero violations (R-14 valid path).
//   - commit-bad-name.json is rejected with ≥1 violation (R-14 invalid path).
//   - tasks-manifest-with-granularity.json validates (R-15 backward-compat).
//   - FormatViolationsWithHints emits the substring "example:" for an
//     enum violation (AC-5 / R-5 contract verified at the schema layer).

import (
	"strings"
	"testing"
)

// TestCommitMinFixture_ValidatesClean asserts the minimal COMMIT fixture
// produces zero CUE violations (R-14 round-trip — valid path).
func TestCommitMinFixture_ValidatesClean(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "commit-min.json")
	res := ValidateWorkflow(payload)
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("commit-min.json should be valid; got %d violations", len(res.Violations))
	}
}

// TestCommitBadNameFixture_IsRejected asserts the invalid fixture with
// `name: "COMMITT"` produces ≥1 CUE violation (R-14 round-trip — invalid path).
func TestCommitBadNameFixture_IsRejected(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "invalid", "commit-bad-name.json")
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatalf("commit-bad-name.json should be rejected; got 0 violations")
	}
	if len(res.Violations) == 0 {
		t.Fatalf("expected ≥1 violation for commit-bad-name.json; got empty slice")
	}
}

// TestTasksManifestWithGranularity_ValidatesClean asserts the fixture
// carrying a granularityWarnings[] array validates cleanly (R-15-schema
// backward-compat — new optional field must not break existing pipeline).
func TestTasksManifestWithGranularity_ValidatesClean(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "tasks-manifest-with-granularity.json")
	res := ValidateWorkflow(payload)
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("tasks-manifest-with-granularity.json should be valid; got %d violations", len(res.Violations))
	}
}

// TestFormatViolationsWithHints_EnumEmitsExample asserts that when
// FormatViolationsWithHints renders an invalid-enum-value violation that
// carries AllowedValues, the output contains the substring "example:" (AC-5 / R-5).
func TestFormatViolationsWithHints_EnumEmitsExample(t *testing.T) {
	vs := []Violation{
		{
			Path:          "steps[0].name",
			Code:          "invalid-enum-value",
			Message:       `invalid value "COMMITT" (does not satisfy "COMMIT")`,
			AddedIn:       "2026-04-24T00:00:00Z",
			Field:         "name",
			AllowedValues: []string{"COMMIT", "PRD", "TASK"},
		},
	}
	out := FormatViolationsWithHints(vs)
	if !strings.Contains(out, "example:") {
		t.Errorf("FormatViolationsWithHints: expected output to contain %q; got:\n%s", "example:", out)
	}
	if !strings.Contains(out, "COMMIT") {
		t.Errorf("FormatViolationsWithHints: expected output to contain a valid enum value; got:\n%s", out)
	}
}

// TestFormatViolationsWithHints_UnknownFieldEmitsExample asserts that an
// unknown-field violation with AllowedFields also emits "example:".
func TestFormatViolationsWithHints_UnknownFieldEmitsExample(t *testing.T) {
	vs := []Violation{
		{
			Path:          "steps[0].commit.conventionalTypo",
			Code:          "unknown-field",
			Message:       `field "conventionalTypo" not allowed`,
			AddedIn:       "2026-04-24T00:00:00Z",
			Field:         "conventionalTypo",
			AllowedFields: []string{"body", "conventionalType", "scope", "subject"},
		},
	}
	out := FormatViolationsWithHints(vs)
	if !strings.Contains(out, "example:") {
		t.Errorf("FormatViolationsWithHints: expected output to contain %q; got:\n%s", "example:", out)
	}
}

// =============================================================
// FR-1 null-admissibility tests (retro2, 2026-05-07)
// =============================================================

// TestCommitBackfillShaNullFixture_ValidatesClean asserts that a COMMIT step
// with backfillSha: null is accepted by the CUE schema (FR-1 fix — previously
// `backfillSha` was absent from #CommitDescriptor entirely, causing save-step
// rejections when agents wrote the field as null).
func TestCommitBackfillShaNullFixture_ValidatesClean(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "commit-backfill-sha-null.json")
	res := ValidateWorkflow(payload)
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("commit-backfill-sha-null.json should be valid; got %d violations", len(res.Violations))
	}
}

// TestCodeReviewAssignedSkillNullFixture_ValidatesClean asserts that a
// CODE_REVIEW finding with assignedSkill: null is accepted by CUE (FR-1 fix
// — previously the field was typed `*"" | string` which rejected explicit null).
func TestCodeReviewAssignedSkillNullFixture_ValidatesClean(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "code-review-assigned-skill-null.json")
	res := ValidateWorkflow(payload)
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("code-review-assigned-skill-null.json should be valid; got %d violations", len(res.Violations))
	}
}

// TestStepIdNull_IsRejected is a regression guard (R-1): stepId: null must
// NOT be accepted even though other fields gain null-admissibility in FR-1.
func TestStepIdNull_IsRejected(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "invalid", "step-id-null.json")
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatalf("step-id-null.json should be rejected; got 0 violations (R-1 regression guard broken)")
	}
}

// TestStepNameNull_IsRejected is a regression guard (R-1): name: null must
// NOT be accepted.
func TestStepNameNull_IsRejected(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "invalid", "step-name-null.json")
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatalf("step-name-null.json should be rejected; got 0 violations (R-1 regression guard broken)")
	}
}

// TestStepStatusNull_IsRejected is a regression guard (R-1): status: null must
// NOT be accepted.
func TestStepStatusNull_IsRejected(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "invalid", "step-status-null.json")
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatalf("step-status-null.json should be rejected; got 0 violations (R-1 regression guard broken)")
	}
}

// TestFormatViolationsWithHints_NoHintsWhenEmpty asserts that violations
// without AllowedValues / AllowedFields render the same as FormatViolations
// (no spurious "example:" token). Backward-compat contract.
func TestFormatViolationsWithHints_NoHintsWhenEmpty(t *testing.T) {
	vs := []Violation{
		{
			Path:    "steps[0].startedAt",
			Code:    "type-mismatch",
			Message: "expected string",
			AddedIn: "2026-04-24T00:00:00Z",
			Field:   "startedAt",
		},
	}
	out := FormatViolationsWithHints(vs)
	if strings.Contains(out, "example:") {
		t.Errorf("FormatViolationsWithHints: unexpected %q in output without hints:\n%s", "example:", out)
	}
	// The base message must still appear.
	if !strings.Contains(out, "type-mismatch") {
		t.Errorf("FormatViolationsWithHints: base violation text missing:\n%s", out)
	}
}
