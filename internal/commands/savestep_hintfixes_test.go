package commands

import (
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/schema"
)

// TestBuildDiagnosticTable_EnumViolation verifies that an invalid-enum-value
// violation produces the correct row with Expected set to the allowed values
// list (FR-2).
func TestBuildDiagnosticTable_EnumViolation(t *testing.T) {
	violations := []schema.Violation{
		{
			Path:          "steps[0].status",
			Code:          "invalid-enum-value",
			Message:       `invalid value "COMMITT" (does not satisfy "PENDING" | "IN_PROGRESS" | "COMPLETED")`,
			Field:         "status",
			AllowedValues: []string{"PENDING", "IN_PROGRESS", "COMPLETED"},
		},
	}
	rows := BuildDiagnosticTable(violations)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row; got %d", len(rows))
	}
	row := rows[0]
	if row.Kind != "invalid-enum" {
		t.Errorf("expected kind=invalid-enum; got %q", row.Kind)
	}
	if row.Got != "COMMITT" {
		t.Errorf("expected got=COMMITT; got %q", row.Got)
	}
	if !strings.Contains(row.Expected, "PENDING") {
		t.Errorf("expected Expected to contain 'PENDING'; got %q", row.Expected)
	}
}

// TestBuildDiagnosticTable_UnknownFieldWithDidYouMean verifies that an
// unknown-field violation produces a DidYouMean suggestion (FR-2 + FR-1).
func TestBuildDiagnosticTable_UnknownFieldWithDidYouMean(t *testing.T) {
	violations := []schema.Violation{
		{
			Path:          "steps[0].execution.scopeAdjustmentz",
			Code:          "unknown-field",
			Message:       `field "scopeAdjustmentz" not allowed`,
			Field:         "scopeAdjustmentz",
			AllowedFields: []string{"scopeAdjustments", "agents", "gates", "invariantsChecked", "nextSteps"},
		},
	}
	rows := BuildDiagnosticTable(violations)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row; got %d", len(rows))
	}
	row := rows[0]
	if row.Kind != "unknown-field" {
		t.Errorf("expected kind=unknown-field; got %q", row.Kind)
	}
	if row.Got != "scopeAdjustmentz" {
		t.Errorf("expected got=scopeAdjustmentz; got %q", row.Got)
	}
	// Should produce a did-you-mean for "scopeAdjustments" (1 char diff).
	if !strings.Contains(row.DidYouMean, "scopeAdjustments") {
		t.Errorf("expected DidYouMean to contain 'scopeAdjustments'; got %q", row.DidYouMean)
	}
}

// TestBuildDiagnosticTable_NoDidYouMeanWhenFarField verifies that a field
// with no close match produces no DidYouMean.
func TestBuildDiagnosticTable_NoDidYouMeanWhenFarField(t *testing.T) {
	violations := []schema.Violation{
		{
			Path:          "steps[0].task.completelyBogusXYZ",
			Code:          "unknown-field",
			Message:       `field "completelyBogusXYZ" not allowed`,
			Field:         "completelyBogusXYZ",
			AllowedFields: []string{"scope", "agents", "gates"},
		},
	}
	rows := BuildDiagnosticTable(violations)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row; got %d", len(rows))
	}
	if rows[0].DidYouMean != "" {
		t.Errorf("expected no DidYouMean for far-off field; got %q", rows[0].DidYouMean)
	}
}

// TestBuildDiagnosticTable_MultipleViolations verifies that all violations
// are collected — not just the first one (FR-2: aggregated diagnostic).
func TestBuildDiagnosticTable_MultipleViolations(t *testing.T) {
	violations := []schema.Violation{
		{
			Path:          "steps[0].status",
			Code:          "invalid-enum-value",
			Message:       `invalid value "DONE"`,
			Field:         "status",
			AllowedValues: []string{"PENDING", "COMPLETED"},
		},
		{
			Path:    "steps[0].startedAt",
			Code:    "type-mismatch",
			Message: "expected string",
			Field:   "startedAt",
		},
	}
	rows := BuildDiagnosticTable(violations)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (all violations collected); got %d", len(rows))
	}
}

// TestFormatDiagnosticTable_ContainsHeaders verifies the table header row
// is present.
func TestFormatDiagnosticTable_ContainsHeaders(t *testing.T) {
	rows := []DiagnosticRow{
		{
			Path:     "steps[0].status",
			Kind:     "invalid-enum",
			Got:      "DONE",
			Expected: "[PENDING, COMPLETED]",
		},
	}
	out := FormatDiagnosticTable(rows)
	if !strings.Contains(out, "PATH") || !strings.Contains(out, "KIND") {
		t.Errorf("diagnostic table missing headers; got:\n%s", out)
	}
}

// TestFormatDiagnosticTable_EmptyReturnsEmpty verifies that empty input
// returns an empty string.
func TestFormatDiagnosticTable_EmptyReturnsEmpty(t *testing.T) {
	out := FormatDiagnosticTable(nil)
	if out != "" {
		t.Errorf("expected empty string for empty rows; got %q", out)
	}
}

// TestFormatDiagnosticTable_IncludesDidYouMean verifies that DidYouMean
// text appears in the Expected column.
func TestFormatDiagnosticTable_IncludesDidYouMean(t *testing.T) {
	rows := []DiagnosticRow{
		{
			Path:       "steps[0].execution.scopeAdjustmentz",
			Kind:       "unknown-field",
			Got:        "scopeAdjustmentz",
			Expected:   "[scopeAdjustments, ...]",
			DidYouMean: "did you mean: scopeAdjustments?",
		},
	}
	out := FormatDiagnosticTable(rows)
	if !strings.Contains(out, "did you mean: scopeAdjustments") {
		t.Errorf("expected DidYouMean text in table; got:\n%s", out)
	}
}
