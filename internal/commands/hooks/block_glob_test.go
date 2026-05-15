package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// makeGlobInput builds a minimal PreToolUse(Glob) JSON payload.
func makeGlobInput(t *testing.T, pattern string) []byte {
	t.Helper()
	payload := map[string]any{
		"tool_name":  "Glob",
		"tool_input": map[string]any{"pattern": pattern},
		"session_id": t.Name(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal glob input: %v", err)
	}
	return data
}

// writeBlockConfig writes a .browzer/config.json with hooks.glob.mode="block"
// into the given workspace root.
func writeBlockConfig(t *testing.T, wsRoot string) {
	t.Helper()
	cfg := `{"hooks":{"glob":{"mode":"block"}}}`
	if err := os.WriteFile(filepath.Join(wsRoot, ".browzer", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write block config: %v", err)
	}
}

// TestBlockGlobDecide_SoftMode_Advisory verifies that in soft mode (default),
// a Glob call outside the workspace config surface gets an advisory envelope
// with permissionDecision:"allow" and exit 0.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestBlockGlobDecide_SoftMode_Advisory(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makeGlobInput(t, "src/**/*.ts")
	code, out := blockGlobDecide(raw)

	if code != internalhooks.ExitAllow {
		t.Errorf("soft mode: exit code = %d, want ExitAllow (%d)", code, internalhooks.ExitAllow)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("soft mode: expected advisory envelope")
	}
	if out.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("soft mode: permissionDecision = %q, want %q",
			out.HookSpecificOutput.PermissionDecision, "allow")
	}
	if out.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("soft mode: hookEventName = %q, want %q",
			out.HookSpecificOutput.HookEventName, "PreToolUse")
	}
}

// TestBlockGlobDecide_BlockMode_DenyEnvelopeExitZero is the canonical
// PreToolUse deny path: the Go subcommand must return ExitAllow (=0) with
// hookSpecificOutput.permissionDecision:"deny" so Claude Code reads the
// structured rationale. Exit 2 would cause Claude Code to discard stdout.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestBlockGlobDecide_BlockMode_DenyEnvelopeExitZero(t *testing.T) {
	wsRoot := setupWorkspaceFixture(t)
	writeBlockConfig(t, wsRoot)

	raw := makeGlobInput(t, "src/**/*.ts")
	code, out := blockGlobDecide(raw)

	if code != internalhooks.ExitAllow {
		t.Errorf("block mode: exit code = %d, want ExitAllow (%d) — canonical exit-0 deny path",
			code, internalhooks.ExitAllow)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("block mode: expected deny envelope on stdout")
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("block mode: permissionDecision = %q, want %q",
			out.HookSpecificOutput.PermissionDecision, "deny")
	}
	if out.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("block mode: hookEventName = %q, want %q",
			out.HookSpecificOutput.HookEventName, "PreToolUse")
	}
	if out.HookSpecificOutput.PermissionDecisionReason == "" {
		t.Error("block mode: permissionDecisionReason must not be empty")
	}
	if out.HookSpecificOutput.AdditionalContext == "" {
		t.Error("block mode: additionalContext must not be empty")
	}
}

// TestBlockGlobDecide_ConfigSurface_Passthrough verifies that patterns
// matching the config surface (e.g. .browzer/config.json paths) are
// passed through without advisory or block.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestBlockGlobDecide_ConfigSurface_Passthrough(t *testing.T) {
	wsRoot := setupWorkspaceFixture(t)
	writeBlockConfig(t, wsRoot)

	// .github/workflows/ matches ConfigSurfaceRE — should passthrough.
	raw := makeGlobInput(t, ".github/workflows/*.yml")
	code, out := blockGlobDecide(raw)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("config surface: exit code = %d, want ExitPassthrough (%d)",
			code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Error("config surface: expected nil envelope")
	}
}

// TestBlockGlobDecide_GateOff_Passthrough verifies the BROWZER_HOOK=off
// kill-switch causes the guard to return ExitPassthrough with no envelope.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestBlockGlobDecide_GateOff_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)
	t.Setenv("BROWZER_HOOK", "off")

	raw := makeGlobInput(t, "src/**/*.ts")
	code, out := blockGlobDecide(raw)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("gate off: exit code = %d, want ExitPassthrough (%d)",
			code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Error("gate off: expected nil envelope")
	}
}

// TestBlockGlobDecide_NonGlobTool_Passthrough verifies that a non-Glob
// tool name is silently passed through.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestBlockGlobDecide_NonGlobTool_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	payload := map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "ls"},
		"session_id": t.Name(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	code, out := blockGlobDecide(data)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("non-Glob: exit code = %d, want ExitPassthrough (%d)",
			code, internalhooks.ExitPassthrough)
	}
	if out != nil {
		t.Error("non-Glob: expected nil envelope")
	}
}
