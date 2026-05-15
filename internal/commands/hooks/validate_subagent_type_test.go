package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// makeSubagentInput builds a minimal PreToolUse(Task) JSON payload.
func makeSubagentInput(t *testing.T, toolName, subagentType string) []byte {
	t.Helper()
	payload := map[string]any{
		"tool_name": toolName,
		"tool_input": map[string]any{
			"subagent_type": subagentType,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return data
}

// TestValidateSubagentType_Allow verifies that a known valid subagent_type passes through.
func TestValidateSubagentType_Allow(t *testing.T) {
	t.Parallel()

	raw := makeSubagentInput(t, "Task", "browzer:fixer")
	code, out := validateSubagentTypeDecide(raw)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("expected ExitPassthrough (%d), got %d", internalhooks.ExitPassthrough, code)
	}
	if out != nil {
		t.Errorf("expected nil envelope on allow path, got %+v", out)
	}
}

// TestValidateSubagentType_NonTaskTool verifies that non-Task tools pass through.
func TestValidateSubagentType_NonTaskTool(t *testing.T) {
	t.Parallel()

	raw := makeSubagentInput(t, "Bash", "browzer:unknown")
	code, out := validateSubagentTypeDecide(raw)

	if code != internalhooks.ExitPassthrough {
		t.Errorf("expected ExitPassthrough (%d), got %d", internalhooks.ExitPassthrough, code)
	}
	if out != nil {
		t.Errorf("expected nil envelope for non-Task tool, got %+v", out)
	}
}

// TestValidateSubagentType_Malformed verifies that malformed JSON passes through.
func TestValidateSubagentType_Malformed(t *testing.T) {
	t.Parallel()

	code, out := validateSubagentTypeDecide([]byte(`not-json`))

	if code != internalhooks.ExitPassthrough {
		t.Errorf("expected ExitPassthrough (%d), got %d", internalhooks.ExitPassthrough, code)
	}
	if out != nil {
		t.Errorf("expected nil envelope for malformed input, got %+v", out)
	}
}

// TestValidateSubagentType_DenyEnvelopeShape is the primary F-024 regression test.
// It asserts that a rejected subagent_type yields:
//   - exit code ExitDeny (2)
//   - a non-nil envelope
//   - hookSpecificOutput.permissionDecision == "deny"
//   - hookSpecificOutput.hookEventName == "PreToolUse"
//   - a non-empty permissionDecisionReason
func TestValidateSubagentType_DenyEnvelopeShape(t *testing.T) {
	t.Parallel()

	raw := makeSubagentInput(t, "Task", "browzer:unknown-agent")
	code, out := validateSubagentTypeDecide(raw)

	if code != internalhooks.ExitDeny {
		t.Errorf("expected ExitDeny (%d), got %d", internalhooks.ExitDeny, code)
	}
	if out == nil {
		t.Fatal("expected non-nil deny envelope")
	}
	if out.HookSpecificOutput == nil {
		t.Fatal("expected non-nil hookSpecificOutput")
	}

	hso := out.HookSpecificOutput
	if hso.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want \"deny\"", hso.PermissionDecision)
	}
	if hso.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want \"PreToolUse\"", hso.HookEventName)
	}
	if hso.PermissionDecisionReason == "" {
		t.Error("permissionDecisionReason must be non-empty")
	}
	if !strings.Contains(hso.PermissionDecisionReason, "browzer:unknown-agent") {
		t.Errorf("reason should contain the rejected value; got: %s", hso.PermissionDecisionReason)
	}
}

// TestValidateSubagentType_DenyEnvelopeContainsLevenshteinSuggestion verifies that
// the deny reason includes the closest Levenshtein suggestion when the value is
// a near-miss of an allowlisted entry.
func TestValidateSubagentType_DenyEnvelopeContainsLevenshteinSuggestion(t *testing.T) {
	t.Parallel()

	// "browzer:fixe" is one edit away from "browzer:fixer" — the suggestion must appear.
	raw := makeSubagentInput(t, "Task", "browzer:fixe")
	code, out := validateSubagentTypeDecide(raw)

	if code != internalhooks.ExitDeny {
		t.Errorf("expected ExitDeny (%d), got %d", internalhooks.ExitDeny, code)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("expected non-nil deny envelope")
	}

	reason := out.HookSpecificOutput.PermissionDecisionReason
	if !strings.Contains(reason, "browzer:fixer") {
		t.Errorf("reason should contain Levenshtein suggestion 'browzer:fixer'; got: %s", reason)
	}
	if !strings.Contains(reason, "Closest match:") {
		t.Errorf("reason should contain 'Closest match:'; got: %s", reason)
	}
}

// TestValidateSubagentType_DenyEnvelopeIsValidJSON verifies that the envelope
// round-trips through JSON without error.
func TestValidateSubagentType_DenyEnvelopeIsValidJSON(t *testing.T) {
	t.Parallel()

	raw := makeSubagentInput(t, "Task", "totally-unknown")
	_, out := validateSubagentTypeDecide(raw)

	if out == nil {
		t.Fatal("expected non-nil deny envelope")
	}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("envelope marshal failed: %v", err)
	}

	var roundtrip validateSubagentDenyEnvelope
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("envelope unmarshal failed: %v", err)
	}
	if roundtrip.HookSpecificOutput == nil {
		t.Error("hookSpecificOutput missing after round-trip")
	}
}

// TestBuildDenyReason_WithSuggestion verifies the "Closest match" message format.
func TestBuildDenyReason_WithSuggestion(t *testing.T) {
	t.Parallel()

	allowlist := []string{"browzer:fixer", "browzer:coder"}
	reason := buildDenyReason("browzer:fixe", allowlist)

	if !strings.Contains(reason, "Closest match: 'browzer:fixer'") {
		t.Errorf("expected 'Closest match: browzer:fixer' in reason; got: %s", reason)
	}
	if !strings.Contains(reason, "Unknown subagent_type 'browzer:fixe'") {
		t.Errorf("expected rejected value in reason; got: %s", reason)
	}
}

// TestBuildDenyReason_WithoutSuggestion verifies the no-suggestion message format.
func TestBuildDenyReason_WithoutSuggestion(t *testing.T) {
	t.Parallel()

	allowlist := []string{"browzer:fixer", "browzer:coder"}
	reason := buildDenyReason("zzzzz-nothing-close", allowlist)

	if strings.Contains(reason, "Closest match:") {
		t.Errorf("expected no suggestion when no close match; got: %s", reason)
	}
	if !strings.Contains(reason, "Unknown subagent_type 'zzzzz-nothing-close'") {
		t.Errorf("expected rejected value in reason; got: %s", reason)
	}
	if !strings.Contains(reason, "Allowed:") {
		t.Errorf("expected 'Allowed:' list in reason; got: %s", reason)
	}
}
