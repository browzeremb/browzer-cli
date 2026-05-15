package hooks

import (
	"encoding/json"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// makeSuggestGrepInput builds a minimal PreToolUse(Grep) JSON payload.
func makeSuggestGrepInput(t *testing.T, toolName, pattern string) []byte {
	t.Helper()
	payload := map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]any{"pattern": pattern},
		"session_id": t.Name(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("makeSuggestGrepInput: marshal failed: %v", err)
	}
	return data
}

// TestSuggestGrepDecide_NonGrepTool_Passthrough verifies that a non-Grep tool
// is silently passed through without an advisory envelope.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestSuggestGrepDecide_NonGrepTool_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makeSuggestGrepInput(t, "Bash", "ls -la")
	code, out := suggestGrepDecide(raw)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("non-Grep: code = %d, want ExitPassthrough (%d)", code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Errorf("non-Grep: expected nil envelope, got %+v", out)
	}
}

// TestSuggestGrepDecide_GateOff_Passthrough verifies the BROWZER_HOOK=off
// kill-switch causes the guard to return ExitPassthrough with no envelope.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestSuggestGrepDecide_GateOff_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)
	t.Setenv("BROWZER_HOOK", "off")

	raw := makeSuggestGrepInput(t, "Grep", "somePattern")
	code, out := suggestGrepDecide(raw)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("gate off: code = %d, want ExitPassthrough (%d)", code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Errorf("gate off: expected nil envelope, got %+v", out)
	}
}

// TestSuggestGrepDecide_ConfigSurface_Passthrough verifies that Grep calls
// targeting the config surface (e.g. .github/workflows/) are passed through
// without an advisory.  ConfigSurfaceRE matches .github, .claude, .vscode, etc.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestSuggestGrepDecide_ConfigSurface_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	payload := map[string]any{
		"tool_name": "Grep",
		"tool_input": map[string]any{
			"pattern": "on:",
			"path":    ".github/workflows/ci.yml",
		},
		"session_id": t.Name(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	code, out := suggestGrepDecide(data)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("config surface: code = %d, want ExitPassthrough (%d)", code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Errorf("config surface: expected nil envelope, got %+v", out)
	}
}

// TestSuggestGrepDecide_GrepInWorkspace_AllowEnvelope verifies that the first
// Grep invocation (non-config-surface, inside a Browzer workspace) returns
// ExitAllow (0) with an advisory envelope containing permissionDecision:"allow".
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestSuggestGrepDecide_GrepInWorkspace_AllowEnvelope(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makeSuggestGrepInput(t, "Grep", "TODO")
	code, out := suggestGrepDecide(raw)

	if code != internalhooks.ExitAllow {
		t.Errorf("advisory: code = %d, want ExitAllow (%d)", code, internalhooks.ExitAllow)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("advisory: expected non-nil envelope with hookSpecificOutput")
	}
	if out.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("advisory: permissionDecision = %q, want \"allow\"",
			out.HookSpecificOutput.PermissionDecision)
	}
	if out.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("advisory: hookEventName = %q, want \"PreToolUse\"",
			out.HookSpecificOutput.HookEventName)
	}
	if out.HookSpecificOutput.AdditionalContext == "" {
		t.Error("advisory: additionalContext must not be empty")
	}
}

// TestSuggestGrepDecide_MalformedJSON_Passthrough verifies graceful degradation
// when stdin is not valid JSON.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestSuggestGrepDecide_MalformedJSON_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	code, out := suggestGrepDecide([]byte(`not-json`))

	if code != internalhooks.ExitPassthrough {
		t.Errorf("malformed: code = %d, want ExitPassthrough (%d)", code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Errorf("malformed: expected nil envelope, got %+v", out)
	}
}
