package hooks

import (
	"encoding/json"
	"os"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// ---- formatStatusSummary unit tests (pure, no exec) ----

// TestFormatStatusSummary_WorkspaceNameAndIndexedAt verifies the preferred
// branch when both workspaceName and indexedAt are present.
func TestFormatStatusSummary_WorkspaceNameAndIndexedAt(t *testing.T) {
	t.Parallel()

	raw, _ := json.Marshal(map[string]any{
		"workspaceName": "my-project",
		"indexedAt":     "2026-05-14T12:00:00Z",
	})

	got := formatStatusSummary(raw)
	want := "[browzer] Workspace: my-project (indexed 2026-05-14T12:00:00Z)"
	if got != want {
		t.Errorf("formatStatusSummary = %q, want %q", got, want)
	}
}

// TestFormatStatusSummary_WorkspaceNameOnly verifies fallback to name-only
// when indexedAt is absent.
func TestFormatStatusSummary_WorkspaceNameOnly(t *testing.T) {
	t.Parallel()

	raw, _ := json.Marshal(map[string]any{
		"workspaceName": "my-project",
	})

	got := formatStatusSummary(raw)
	want := "[browzer] Workspace: my-project"
	if got != want {
		t.Errorf("formatStatusSummary = %q, want %q", got, want)
	}
}

// TestFormatStatusSummary_WorkspaceIDFallback verifies the workspace_id
// branch when name is absent.
func TestFormatStatusSummary_WorkspaceIDFallback(t *testing.T) {
	t.Parallel()

	raw, _ := json.Marshal(map[string]any{
		"workspaceId": "abc123",
	})

	got := formatStatusSummary(raw)
	want := "[browzer] Workspace ID: abc123"
	if got != want {
		t.Errorf("formatStatusSummary = %q, want %q", got, want)
	}
}

// TestFormatStatusSummary_AllFieldsEmpty verifies the default stub when no
// recognisable fields are present.
func TestFormatStatusSummary_AllFieldsEmpty(t *testing.T) {
	t.Parallel()

	raw, _ := json.Marshal(map[string]any{"unknown": "value"})

	got := formatStatusSummary(raw)
	want := "[browzer] Workspace status available."
	if got != want {
		t.Errorf("formatStatusSummary = %q, want %q", got, want)
	}
}

// TestFormatStatusSummary_NonJSONInput verifies the non-JSON fallback stub.
func TestFormatStatusSummary_NonJSONInput(t *testing.T) {
	t.Parallel()

	got := formatStatusSummary([]byte(`not-json`))
	if got == "" {
		t.Error("non-JSON: expected non-empty fallback stub")
	}
}

// ---- jsonStringField unit tests (pure, no exec) ----

// TestJSONStringField_FirstKeyWins verifies the function returns the first
// non-empty value encountered across the candidate keys.
func TestJSONStringField_FirstKeyWins(t *testing.T) {
	t.Parallel()

	raw, _ := json.Marshal(map[string]any{
		"a": "first",
		"b": "second",
	})
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)

	got := jsonStringField(obj, "a", "b")
	if got != "first" {
		t.Errorf("jsonStringField = %q, want \"first\"", got)
	}
}

// TestJSONStringField_MissingKeyReturnsEmpty verifies "" is returned when none
// of the candidate keys are present.
func TestJSONStringField_MissingKeyReturnsEmpty(t *testing.T) {
	t.Parallel()

	raw, _ := json.Marshal(map[string]any{"x": "value"})
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)

	got := jsonStringField(obj, "missing")
	if got != "" {
		t.Errorf("jsonStringField = %q, want empty string", got)
	}
}

// ---- sessionStartDecide integration-style tests ----

// TestSessionStartDecide_NotInWorkspace_Passthrough verifies that the guard
// returns ExitPassthrough when the cwd is not a Browzer workspace.
//
// Note: changes HOME + cwd via t.Setenv/t.Cleanup — t.Parallel() would panic.
func TestSessionStartDecide_NotInWorkspace_Passthrough(t *testing.T) {
	// Create an empty tmpdir with no .browzer directory — simulates a
	// non-workspace environment.
	tmp := t.TempDir()

	// Redirect HOME to a dir with no .browzer/credentials so
	// IsInBrowzerWorkspace returns false.
	t.Setenv("HOME", tmp)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir tmpdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	raw, _ := json.Marshal(map[string]any{
		"hook_event_name": "SessionStart",
		"session_id":      "test-session",
		"transcript_path": "/tmp/transcript.json",
	})

	code, out := sessionStartDecide(raw)

	// Without a workspace, the guard must passthrough.
	if code != internalhooks.ExitPassthrough {
		t.Errorf("no workspace: code = %d, want ExitPassthrough (%d)", code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Errorf("no workspace: expected nil envelope, got %+v", out)
	}
}
