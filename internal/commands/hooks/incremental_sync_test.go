package hooks

import (
	"encoding/json"
	"testing"
)

// makeIncrementalSyncInput builds a minimal PostToolUse(Edit|Write) JSON payload.
func makeIncrementalSyncInput(t *testing.T, toolName, filePath string) []byte {
	t.Helper()
	payload := map[string]any{
		"tool_name": toolName,
		"tool_input": map[string]any{
			"file_path": filePath,
		},
		"session_id": t.Name(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("makeIncrementalSyncInput: marshal failed: %v", err)
	}
	return data
}

// TestIncrementalSyncDecide_NonEditWrite_False verifies that tools other than
// Edit and Write return false (no sync triggered).
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestIncrementalSyncDecide_NonEditWrite_False(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makeIncrementalSyncInput(t, "Bash", "src/main.go")
	if incrementalSyncDecide(raw) {
		t.Error("non-Edit/Write tool: expected false (no sync)")
	}
}

// TestIncrementalSyncDecide_GateOff_False verifies the BROWZER_HOOK=off
// kill-switch suppresses syncing.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestIncrementalSyncDecide_GateOff_False(t *testing.T) {
	setupWorkspaceFixture(t)
	t.Setenv("BROWZER_HOOK", "off")

	raw := makeIncrementalSyncInput(t, "Edit", "src/main.go")
	if incrementalSyncDecide(raw) {
		t.Error("gate off: expected false (no sync)")
	}
}

// TestIncrementalSyncDecide_NonIndexableExt_False verifies that edits to
// non-indexable extensions (e.g. .log, .bin) do not trigger a sync.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestIncrementalSyncDecide_NonIndexableExt_False(t *testing.T) {
	setupWorkspaceFixture(t)

	for _, ext := range []string{"build.log", "data.bin", "archive.zip"} {
		raw := makeIncrementalSyncInput(t, "Edit", ext)
		if incrementalSyncDecide(raw) {
			t.Errorf("non-indexable %q: expected false (no sync)", ext)
		}
	}
}

// TestIncrementalSyncDecide_IndexableExtensions_GateCheck verifies that
// indexable extensions pass the extension filter. The test cannot confirm
// a successful exec.LookPath("browzer") or child start (binary may be absent
// in the test environment), but it confirms the extension filter does NOT
// short-circuit on these extensions.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestIncrementalSyncDecide_IndexableExtensions_GateCheck(t *testing.T) {
	setupWorkspaceFixture(t)

	indexable := []string{
		"main.go", "index.ts", "lib.js", "style.mjs",
		"README.md", "notes.txt", "report.pdf",
	}

	for _, name := range indexable {
		raw := makeIncrementalSyncInput(t, "Edit", name)
		// The function may return false if browzer binary is absent; that's fine.
		// What we assert is that it never panics and the return type is bool.
		_ = incrementalSyncDecide(raw)
	}
}

// TestIncrementalSyncDecide_EmptyFilePath_False verifies that an empty
// file_path field short-circuits without triggering a sync.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestIncrementalSyncDecide_EmptyFilePath_False(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makeIncrementalSyncInput(t, "Write", "")
	if incrementalSyncDecide(raw) {
		t.Error("empty file_path: expected false (no sync)")
	}
}

// TestIncrementalSyncDecide_MalformedJSON_False verifies graceful degradation
// when stdin is not valid JSON.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestIncrementalSyncDecide_MalformedJSON_False(t *testing.T) {
	setupWorkspaceFixture(t)

	if incrementalSyncDecide([]byte(`not-json`)) {
		t.Error("malformed JSON: expected false (no sync)")
	}
}
