package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validReviewEntryJSON is a well-formed review history entry payload using
// the canonical field names (at + action).
const validReviewEntryJSON = `{
  "role": "reviewer",
  "action": "approved",
  "comment": "Looks good",
  "at": "2026-04-29T01:00:00Z"
}`

// validReviewEntryAliasJSON is a well-formed payload using the legacy aliases
// (timestamp + decision) to verify backward-compat acceptance.
const validReviewEntryAliasJSON = `{
  "role": "reviewer",
  "decision": "approved",
  "comment": "Looks good",
  "timestamp": "2026-04-29T01:00:00Z"
}`

// TestAppendReviewHistory_ValidEntryAppendsToStep verifies that
// `browzer workflow append-review-history <stepId> --payload <file>`
// appends the review entry to the step's reviewHistory[] and leaves
// all other step fields untouched.
// Covers T3-T-6 (valid entry).
func TestAppendReviewHistory_ValidEntryAppendsToStep(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	payloadFile := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(payloadFile, []byte(validReviewEntryJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture original step fields before mutation (except reviewHistory).
	data, _ := os.ReadFile(wfPath)
	var beforeDoc map[string]any
	_ = json.Unmarshal(data, &beforeDoc)
	beforeSteps := beforeDoc["steps"].([]any)
	beforeStep := beforeSteps[0].(map[string]any)
	beforeStatus := beforeStep["status"]

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-review-history", "STEP_01_BRAINSTORMING",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("append-review-history with valid entry should exit 0, got: %v\nstderr: %s",
			err, stderr.String())
	}

	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var afterDoc map[string]any
	if err := json.Unmarshal(after, &afterDoc); err != nil {
		t.Fatalf("parse workflow after append-review-history: %v", err)
	}

	afterSteps := afterDoc["steps"].([]any)
	afterStep := afterSteps[0].(map[string]any)

	// reviewHistory must have grown by 1.
	reviewHistory, ok := afterStep["reviewHistory"].([]any)
	if !ok || len(reviewHistory) < 1 {
		t.Errorf("expected reviewHistory to have at least 1 entry, got: %v", afterStep["reviewHistory"])
	}

	// Other step fields must be untouched.
	if afterStep["status"] != beforeStatus {
		t.Errorf("status was mutated: before=%v after=%v", beforeStatus, afterStep["status"])
	}
}

// TestAppendReviewHistory_InvalidEntryRejectedNoMutation verifies that
// an invalid review entry (missing required fields or bad shape) exits
// non-zero and leaves the workflow.json unchanged.
// Covers T3-T-6 (invalid shape).
func TestAppendReviewHistory_InvalidEntryRejectedNoMutation(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	// Entry missing required fields — just an empty object.
	invalidEntry := `{}`
	payloadFile := filepath.Join(t.TempDir(), "bad_review.json")
	if err := os.WriteFile(payloadFile, []byte(invalidEntry), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-review-history", "STEP_01_BRAINSTORMING",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for invalid review entry, got nil error")
	}

	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("append-review-history with invalid entry must not mutate workflow.json")
	}
}

// TestAppendReviewHistory_NonExistentStepExitsNonZero verifies that
// targeting a stepId that doesn't exist exits non-zero.
// Covers T3-T-6 (missing step branch).
func TestAppendReviewHistory_NonExistentStepExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	payloadFile := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(payloadFile, []byte(validReviewEntryJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-review-history", "STEP_99_NONEXISTENT",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for non-existent step, got nil error")
	}
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "STEP_99_NONEXISTENT") {
		t.Errorf("expected stderr to name missing stepId, got: %q", stderrStr)
	}
}

// TestAppendReviewHistory_AliasPayloadAccepted verifies that the legacy
// decision/timestamp aliases are still accepted (backward-compat).
// Covers F-SE-1 alias-acceptance path.
func TestAppendReviewHistory_AliasPayloadAccepted(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	payloadFile := filepath.Join(t.TempDir(), "review_alias.json")
	if err := os.WriteFile(payloadFile, []byte(validReviewEntryAliasJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-review-history", "STEP_01_BRAINSTORMING",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("alias payload (decision/timestamp) should be accepted, got: %v\nstderr: %s",
			err, stderr.String())
	}
}

// TestAppendReviewHistory_DecisionAliasGoesThroughAllowlist verifies that
// the "decision" alias path goes through the legalReviewActions allowlist
// so that an invalid value is rejected even when "decision" is used instead
// of "action". Fixes F-SE-1 / F-sec-11.
func TestAppendReviewHistory_DecisionAliasGoesThroughAllowlist(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	// Use the "decision" alias with an invalid value — must be rejected.
	invalidDecisionEntry := `{
  "role": "reviewer",
  "decision": "arbitrary_invalid_value",
  "at": "2026-04-29T01:00:00Z"
}`
	payloadFile := filepath.Join(t.TempDir(), "bad_decision.json")
	if err := os.WriteFile(payloadFile, []byte(invalidDecisionEntry), 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-review-history", "STEP_01_BRAINSTORMING",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for invalid decision alias value, got nil")
	}
	if !strings.Contains(err.Error(), "arbitrary_invalid_value") {
		t.Errorf("expected error to name the invalid value, got: %v", err)
	}

	// File must not be mutated.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("workflow.json must not be mutated when decision alias carries an invalid value")
	}
}
