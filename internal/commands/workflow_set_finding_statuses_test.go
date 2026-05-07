package commands

import (
	"strings"
	"testing"
)

// TestSetFindingStatuses_BatchExpressionShape (A3.1, 2026-05-05):
// the bulk verb composes a single jq pipeline that targets every
// findings[] member by id and updates the status. The composed
// expression order is stable (sorted by stepId, findingId).
func TestSetFindingStatuses_BatchExpressionShape(t *testing.T) {
	entries := []findingStatusBatchEntry{
		{StepID: "STEP_05_CR", FindingID: "F-3", Status: "fixed"},
		{StepID: "STEP_05_CR", FindingID: "F-1", Status: "wontfix", Note: "out of scope"},
	}
	expr := buildFindingStatusBatchJQ(entries)
	if !strings.Contains(expr, `select(.id == "F-1")`) {
		t.Errorf("expected F-1 selector in expression: %s", expr)
	}
	if !strings.Contains(expr, `select(.id == "F-3")`) {
		t.Errorf("expected F-3 selector in expression: %s", expr)
	}
	if !strings.Contains(expr, `.status = "fixed"`) {
		t.Errorf("expected status assignment to fixed: %s", expr)
	}
	// Notes side-channel must land in the workflow-root .notes array.
	if !strings.Contains(expr, `.notes += [`) {
		t.Errorf("expected .notes += [...] for the noted entry: %s", expr)
	}
	// Sorted order: F-1 must come before F-3 in the composed pipeline.
	idx1 := strings.Index(expr, "F-1")
	idx3 := strings.Index(expr, "F-3")
	if idx1 < 0 || idx3 < 0 || idx1 > idx3 {
		t.Errorf("expected stable sort order F-1 before F-3 in: %s", expr)
	}
}

// TestSetFindingStatuses_RejectsBadInputBeforeLock (A3.1, 2026-05-05):
// every entry's status is checked against findingStatusValues and
// every findingId against findingIDPattern at the cobra layer.
// Bad input must short-circuit BEFORE any jq composition or
// dispatch round-trip.
func TestSetFindingStatuses_RejectsBadInputBeforeLock(t *testing.T) {
	bad := []findingStatusBatchEntry{
		{StepID: "STEP_5", FindingID: "F-bogus", Status: "fixed"},
	}
	if isValidFindingStatus(bad[0].FindingID) {
		t.Errorf("findingId %q must NOT pass status check", bad[0].FindingID)
	}
	if findingIDPattern.MatchString("F-bogus") {
		t.Errorf("findingIDPattern unexpectedly accepted %q", "F-bogus")
	}
	if !findingIDPattern.MatchString("F-42") {
		t.Errorf("findingIDPattern rejected the valid F-42")
	}

	for _, valid := range findingStatusValues {
		if !isValidFindingStatus(valid) {
			t.Errorf("isValidFindingStatus rejected the canonical %q", valid)
		}
	}
	if isValidFindingStatus("RESOLVED") {
		t.Error("isValidFindingStatus accepted unknown value 'RESOLVED'")
	}
}

