package commands

import (
	"bytes"
	"strings"
	"testing"
)

// TestIngest_NoWorkspaceFlag_ExitsNonZero asserts that `browzer ingest`
// invoked without --workspace exits non-zero and prints a message that
// references the workspace requirement (T-01-07).
//
// RED: The `ingest` command does not exist yet.  NewRootCommand does not
// register it, so root.Find([]string{"ingest"}) will return nil and the
// test will fail with "ingest command not registered".
func TestIngest_NoWorkspaceFlag_ExitsNonZero(t *testing.T) {
	root := NewRootCommand("test")

	// Confirm the command is registered — this is the first failure point.
	cmd, _, err := root.Find([]string{"ingest"})
	if err != nil || cmd == nil || cmd.Name() != "ingest" {
		t.Fatal("ingest command not registered on root — add registerIngest(root) to NewRootCommand")
	}

	// Capture stderr + stdout so we can assert on the error message.
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)

	root.SetArgs([]string{"ingest"})
	execErr := root.Execute()

	// Must exit non-zero (cobra propagates RunE errors).
	if execErr == nil {
		t.Fatal("expected non-zero exit when --workspace is omitted, got nil error")
	}

	// The combined output must reference the workspace requirement.
	combined := outBuf.String() + errBuf.String()
	if !strings.Contains(strings.ToLower(combined), "workspace") {
		t.Errorf("expected output to reference 'workspace', got:\n%s", combined)
	}
}

// TestIngest_WorkspaceFlagRegistered asserts that the `ingest` command
// exposes a --workspace flag (T-01-07 prerequisite).
//
// RED: `ingest` command doesn't exist yet, so Flags().Lookup will panic or
// return nil.
func TestIngest_WorkspaceFlagRegistered(t *testing.T) {
	root := NewRootCommand("test")

	cmd, _, err := root.Find([]string{"ingest"})
	if err != nil || cmd == nil || cmd.Name() != "ingest" {
		t.Fatal("ingest command not registered — cannot check flags")
	}

	flag := cmd.Flags().Lookup("workspace")
	if flag == nil {
		t.Fatal("--workspace flag not registered on ingest command")
	}
}

// TestIngest_WithWorkspace_DoesNotErrorOnMissingFlag asserts that providing
// --workspace silences the "workspace required" error, even if the command
// then fails for an unrelated reason (auth/network).  This confirms the flag
// is the discriminator for the workspace-required guard.
//
// RED: `ingest` command doesn't exist yet.
func TestIngest_WithWorkspace_DoesNotPrintWorkspaceRequiredMessage(t *testing.T) {
	root := NewRootCommand("test")

	cmd, _, err := root.Find([]string{"ingest"})
	if err != nil || cmd == nil || cmd.Name() != "ingest" {
		t.Skip("ingest command not registered — skipping flag-discriminator test")
	}

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)

	// Provide --workspace but point at a non-existent server so the command
	// fails fast without real network I/O.
	root.SetArgs([]string{"ingest", "--workspace", "ws-test-123", "--server", "http://127.0.0.1:19999"})
	// Execute may succeed or fail; we only care that the output does NOT
	// contain a "workspace required" error message.
	_ = root.Execute()

	combined := strings.ToLower(outBuf.String() + errBuf.String())
	if strings.Contains(combined, "workspace is required") || strings.Contains(combined, "--workspace is required") {
		t.Errorf("unexpected 'workspace required' message when --workspace IS provided:\n%s", combined)
	}
}
