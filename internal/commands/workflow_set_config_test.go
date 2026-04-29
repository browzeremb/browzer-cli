package commands

import (
	"bytes"
	"strings"
	"testing"
)

// TestSetConfig_ModeRoundTrip verifies that
// `browzer workflow set-config mode review` writes the value and sets setAt,
// and a subsequent `browzer workflow get-config mode` returns "review".
// Covers T3-T-5.
func TestSetConfig_ModeRoundTrip(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	// First: set mode to "review".
	var setStdout, setStderr bytes.Buffer
	setRoot := buildWorkflowCommand(&setStdout, &setStderr)
	setRoot.SetArgs([]string{
		"workflow", "set-config", "mode", "review",
		"--workflow", wfPath,
	})
	if err := setRoot.Execute(); err != nil {
		t.Fatalf("set-config mode review should exit 0, got: %v\nstderr: %s", err, setStderr.String())
	}

	// Second: get-config mode should now return "review".
	var getStdout, getStderr bytes.Buffer
	getRoot := buildWorkflowCommand(&getStdout, &getStderr)
	getRoot.SetArgs([]string{
		"workflow", "get-config", "mode",
		"--workflow", wfPath,
	})
	if err := getRoot.Execute(); err != nil {
		t.Fatalf("get-config mode after set-config should exit 0, got: %v\nstderr: %s", err, getStderr.String())
	}

	got := strings.TrimSpace(getStdout.String())
	if got != "review" {
		t.Errorf("expected get-config mode to return 'review' after set-config, got %q", got)
	}
}

// TestSetConfig_SetsSetAt verifies that after set-config, the .config.setAt
// field is updated to a non-empty ISO timestamp.
// Covers T3-T-5 (setAt stamp sub-case).
func TestSetConfig_SetsSetAt(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-config", "mode", "review",
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("set-config: %v\nstderr: %s", err, stderr.String())
	}

	// Read setAt via get-config.
	var getStdout, getStderr bytes.Buffer
	getRoot := buildWorkflowCommand(&getStdout, &getStderr)
	getRoot.SetArgs([]string{
		"workflow", "get-config", "setAt",
		"--workflow", wfPath,
	})
	if err := getRoot.Execute(); err != nil {
		t.Fatalf("get-config setAt: %v\nstderr: %s", err, getStderr.String())
	}

	setAt := strings.TrimSpace(getStdout.String())
	if setAt == "" {
		t.Error("expected setAt to be a non-empty timestamp after set-config, got empty")
	}
}

// TestSetConfig_IllegalValueExitsNonZero verifies that set-config with an
// illegal value for a known key (e.g. mode=invalid) exits non-zero.
// Covers T3-T-5 (validation of known keys).
func TestSetConfig_IllegalValueExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "set-config", "mode", "ILLEGAL_MODE_VALUE",
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for illegal config mode value, got nil error")
	}
}
