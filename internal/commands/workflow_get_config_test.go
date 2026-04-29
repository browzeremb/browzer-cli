package commands

import (
	"bytes"
	"strings"
	"testing"
)

// TestGetConfig_ModeReturnsUnquoted verifies that `browzer workflow get-config mode`
// returns "autonomous" or "review" as an unquoted string (raw value, not JSON).
// Covers T1-T-r4: get-config mode returns autonomous|review unquoted.
func TestGetConfig_ModeReturnsUnquoted(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON) // mode is "autonomous"

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out != "autonomous" {
		t.Errorf("expected %q, got %q", "autonomous", out)
	}
	// Must NOT be quoted as JSON string.
	if strings.HasPrefix(out, `"`) {
		t.Errorf("value should not be JSON-quoted, got %q", out)
	}
}

// TestGetConfig_ReviewModeReturnsUnquoted verifies the same for "review" mode.
// Covers T1-T-r4: get-config mode returns review unquoted.
func TestGetConfig_ReviewModeReturnsUnquoted(t *testing.T) {
	reviewContent := strings.ReplaceAll(minimalWorkflowJSON, `"mode": "autonomous"`, `"mode": "review"`)
	wfPath := writeWorkflowFile(t, reviewContent)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out != "review" {
		t.Errorf("expected %q, got %q", "review", out)
	}
}

// TestGetConfig_FieldNestedPath verifies that --field config-style nested
// paths (e.g. setAt) are supported.
// Covers T1-T-r4: --field config-style nested paths supported.
func TestGetConfig_FieldNestedPath(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "setAt", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Error("expected non-empty value for setAt, got empty")
	}
	// setAt is a timestamp string — should be unquoted.
	if strings.HasPrefix(out, `"`) {
		t.Errorf("setAt should be unquoted, got %q", out)
	}
}

// TestGetConfig_UnknownKeyExitsNonZero verifies that requesting an unknown
// config key exits non-zero.
// Covers T1-T-r4: error path for unknown config key.
func TestGetConfig_UnknownKeyExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "nonExistentKey", "--workflow", wfPath})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for unknown config key, got nil error")
	}
}
