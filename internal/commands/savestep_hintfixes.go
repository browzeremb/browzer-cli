package commands

// savestep_hintfixes.go — aggregated diagnostic table for save-step --hint-fixes
//
// FR-2: when validation fails, collect ALL CUE violations and emit them as a
// structured table with: path, kind, got (the submitted value snippet), and
// expected/closest. Unknown-field violations include did-you-mean suggestions
// via Levenshtein distance ≤ 2.
//
// FR-3: --hint-fixes worked examples come from:
//   (a) .browzer/.schema-cache/<PHASE>.json examples[] if present
//   (b) the phase's template.md JSON fences
//
// This file is intentionally separated from workflow_save_step.go so
// TASK_09 (browzer codereview aggregate) can import the same diagnostic
// rendering logic from the diagnostic package without a circular dependency.

import (
	"fmt"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/diagnostic"
	"github.com/browzeremb/browzer-cli/internal/schema"
)

// DiagnosticRow is one row in the aggregated violation table.
type DiagnosticRow struct {
	Path       string // JSON-pointer-like path
	Kind       string // unknown-field | invalid-enum | type-mismatch | missing-required | structural-error
	Got        string // the submitted value snippet (best-effort)
	Expected   string // expected/closest value or list
	DidYouMean string // non-empty when kind=unknown-field and Levenshtein candidate found
}

// BuildDiagnosticTable converts a slice of schema.Violation into a
// []DiagnosticRow suitable for tabular rendering. The function is pure (no
// IO) and unit-testable without CUE.
func BuildDiagnosticTable(violations []schema.Violation) []DiagnosticRow {
	rows := make([]DiagnosticRow, 0, len(violations))
	for _, v := range violations {
		row := DiagnosticRow{
			Path: v.Path,
			Kind: mapViolationKind(v.Code),
		}

		switch v.Code {
		case "invalid-enum-value":
			row.Expected = formatAllowedValues(v.AllowedValues)
			// Extract the submitted value from the message
			// (CUE typically emits: `invalid value "X" (does not satisfy "Y")`).
			row.Got = extractGotFromEnumMessage(v.Message)
		case "unknown-field":
			row.Got = extractFieldNameFromMessage(v.Message, v.Field)
			row.Expected = formatAllowedFields(v.AllowedFields)
			if len(v.AllowedFields) > 0 {
				suggestions := diagnostic.DidYouMean(row.Got, v.AllowedFields)
				row.DidYouMean = diagnostic.FormatSuggestion(suggestions)
			}
		case "type-mismatch":
			row.Got = extractTypeMismatchGot(v.Message)
			row.Expected = extractTypeMismatchExpected(v.Message)
		case "missing-required-field":
			row.Expected = v.Field + " (required)"
		default:
			row.Got = ""
			row.Expected = v.Message
		}

		rows = append(rows, row)
	}
	return rows
}

// FormatDiagnosticTable renders the diagnostic rows as a text table for
// stderr output. The format mirrors the task brief spec (path | kind | got |
// expected).
func FormatDiagnosticTable(rows []DiagnosticRow) string {
	if len(rows) == 0 {
		return ""
	}

	const sep = " | "
	header := fmt.Sprintf("%-40s | %-22s | %-20s | %s", "PATH", "KIND", "GOT", "EXPECTED/CLOSEST")
	divider := strings.Repeat("-", len(header))

	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(divider)
	b.WriteByte('\n')

	for _, row := range rows {
		got := row.Got
		expected := row.Expected
		if row.DidYouMean != "" {
			expected = expected + " [" + row.DidYouMean + "]"
		}

		line := fmt.Sprintf("%-40s%s%-22s%s%-20s%s%s",
			truncate(row.Path, 40), sep,
			truncate(row.Kind, 22), sep,
			truncate(got, 20), sep,
			expected,
		)
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return b.String()
}

// truncate returns s capped to maxLen characters (appending "…" when truncated).
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}

// mapViolationKind maps a CUE violation code to the canonical kind label.
func mapViolationKind(code string) string {
	switch code {
	case "unknown-field":
		return "unknown-field"
	case "invalid-enum-value":
		return "invalid-enum"
	case "type-mismatch":
		return "type-mismatch"
	case "missing-required-field":
		return "missing-required"
	case "structural-error":
		return "structural-error"
	default:
		return code
	}
}

// formatAllowedValues formats a list of allowed enum values as "[a, b, c]"
// or returns "(none)" when the list is empty.
func formatAllowedValues(vals []string) string {
	if len(vals) == 0 {
		return "(none)"
	}
	return "[" + strings.Join(vals, ", ") + "]"
}

// formatAllowedFields formats a list of allowed field names.
func formatAllowedFields(fields []string) string {
	if len(fields) == 0 {
		return "(none)"
	}
	return "[" + strings.Join(fields, ", ") + "]"
}

// extractGotFromEnumMessage attempts to extract the submitted value from a
// CUE enum violation message of the form:
// `invalid value "X" (does not satisfy ...)` → "X"
func extractGotFromEnumMessage(msg string) string {
	// CUE typically formats: `invalid value "foo" (does not satisfy "bar")`
	if idx := strings.Index(msg, `"`); idx >= 0 {
		rest := msg[idx+1:]
		if end := strings.Index(rest, `"`); end >= 0 {
			return rest[:end]
		}
	}
	return ""
}

// extractFieldNameFromMessage extracts the field name from an unknown-field
// message. Falls back to the pre-populated v.Field value.
func extractFieldNameFromMessage(msg, field string) string {
	if field != "" {
		return field
	}
	// CUE: `field "foo" not allowed`
	if idx := strings.Index(msg, `"`); idx >= 0 {
		rest := msg[idx+1:]
		if end := strings.Index(rest, `"`); end >= 0 {
			return rest[:end]
		}
	}
	return msg
}

// extractTypeMismatchGot attempts to extract the actual type from a CUE
// type-mismatch message. Returns "" when extraction fails.
func extractTypeMismatchGot(msg string) string {
	// CUE type-mismatch: `conflicting values X and Y` or `cannot use X (type A) as type B`
	if idx := strings.Index(msg, "cannot use "); idx >= 0 {
		rest := msg[idx+len("cannot use "):]
		if end := strings.Index(rest, " "); end >= 0 {
			return rest[:end]
		}
	}
	return ""
}

// extractTypeMismatchExpected extracts the expected type from a CUE type-mismatch message.
func extractTypeMismatchExpected(msg string) string {
	if idx := strings.Index(msg, "as type "); idx >= 0 {
		return strings.TrimSpace(msg[idx+len("as type "):])
	}
	if idx := strings.Index(msg, "expected "); idx >= 0 {
		return strings.TrimSpace(msg[idx+len("expected "):])
	}
	return msg
}
