package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestTruncationAudit_AppendsWarningEntry verifies that
// `browzer workflow truncation-audit <stepId> --payload <file>` appends a
// truncation-suspected warning entry to the step's warnings[] array.
func TestTruncationAudit_AppendsWarningEntry(t *testing.T) {
	wfPath := writeWorkflowFile(t, taskWorkflowForModelOverride) // reuse TASK step fixture

	// Write the payload to a temp file.
	payload := `{
  "filesModified": ["apps/api/src/routes/ask.ts", "packages/core/src/search/chain.ts"],
  "filesCreated": ["apps/api/src/new-middleware.ts"],
  "filesDeleted": []
}`
	payloadFile := filepath.Join(t.TempDir(), "truncation.json")
	if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "truncation-audit", "STEP_05_TASK_01",
		"--payload", payloadFile,
		"--last-checkpoint", "Step 3: completed schema validation",
		"--workflow", wfPath, "--sync",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("truncation-audit: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	warnings, ok := step["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("expected at least 1 warning entry, got: %v", step["warnings"])
	}

	w, ok := warnings[0].(map[string]any)
	if !ok {
		t.Fatalf("expected warning to be an object, got %T", warnings[0])
	}
	if w["kind"] != "truncation-suspected" {
		t.Errorf("expected warning.kind = truncation-suspected, got %q", w["kind"])
	}
	if w["at"] == nil || w["at"] == "" {
		t.Error("expected warning.at to be auto-stamped")
	}
	if w["lastCheckpoint"] != "Step 3: completed schema validation" {
		t.Errorf("expected lastCheckpoint to be set, got %q", w["lastCheckpoint"])
	}
	if w["remediation"] == nil || w["remediation"] == "" {
		t.Error("expected warning.remediation to be set")
	}
	// Check filesModified is a 2-element array.
	fm, ok := w["filesModified"].([]any)
	if !ok || len(fm) != 2 {
		t.Errorf("expected filesModified to have 2 entries, got: %v", w["filesModified"])
	}
	// Check filesCreated is a 1-element array.
	fc, ok := w["filesCreated"].([]any)
	if !ok || len(fc) != 1 {
		t.Errorf("expected filesCreated to have 1 entry, got: %v", w["filesCreated"])
	}
}

// TestTruncationAudit_MultipleWarningsAppend verifies that calling
// truncation-audit twice appends two separate warning entries (not overwrites).
func TestTruncationAudit_MultipleWarningsAppend(t *testing.T) {
	wfPath := writeWorkflowFile(t, taskWorkflowForModelOverride)

	payload := `{"filesModified": ["pkg/foo.go"], "filesCreated": [], "filesDeleted": []}`
	payloadFile := filepath.Join(t.TempDir(), "truncation.json")
	if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	runCmd := func() {
		t.Helper()
		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommandT(t, &stdout, &stderr)
		root.SetArgs([]string{
			"workflow", "truncation-audit", "STEP_05_TASK_01",
			"--payload", payloadFile,
			"--workflow", wfPath, "--sync",
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("truncation-audit: %v\nstderr: %s", err, stderr.String())
		}
	}

	runCmd()
	runCmd()

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	warnings := step["warnings"].([]any)

	if len(warnings) != 2 {
		t.Errorf("expected 2 warning entries after 2 calls, got %d", len(warnings))
	}
}

// TestTruncationAudit_NonExistentStepExitsNonZero verifies that targeting
// a missing stepId exits non-zero.
func TestTruncationAudit_NonExistentStepExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	payload := `{"filesModified": [], "filesCreated": [], "filesDeleted": []}`
	payloadFile := filepath.Join(t.TempDir(), "truncation.json")
	if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "truncation-audit", "STEP_99_NONEXISTENT",
		"--payload", payloadFile,
		"--workflow", wfPath, "--sync",
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for non-existent step, got nil")
	}
}
