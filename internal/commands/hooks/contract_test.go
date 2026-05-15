package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// makeContractInput returns a JSON PreToolUse envelope for the given command.
func makeContractInput(t *testing.T, cmd, sessionID string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": cmd},
		"session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("makeContractInput: marshal failed: %v", err)
	}
	return raw
}

// cleanContractState removes the on-disk offense-seen state for a given
// sessionID so tests that exercise the "first offense" path are idempotent
// across repeated runs. Covers BOTH the legacy JSON ledger file (left over
// from pre-sentinel runs) AND the current per-(sessionID) directory.
func cleanContractState(t *testing.T, sessionID string) {
	t.Helper()
	legacy := filepath.Join(os.TempDir(), ".browzer-guard", sessionID+".contract.json")
	dir := filepath.Join(os.TempDir(), ".browzer-guard", sessionID)
	_ = os.Remove(legacy)
	_ = os.RemoveAll(dir)
	t.Cleanup(func() {
		_ = os.Remove(legacy)
		_ = os.RemoveAll(dir)
	})
}

// TestContractOffenseSeen_Sentinel pins FR-03 / AC-03: the offense ledger
// uses an O_CREATE|O_EXCL sentinel keyed by (sessionID, sub). First call
// for a (session, sub) pair returns false; repeats return true; distinct
// subs under the same session are independent first-offense decisions;
// no JSON ledger is written on the fast path.
func TestContractOffenseSeen_Sentinel(t *testing.T) {
	tmpDir := t.TempDir()
	restore := WithTmpDir(func() string { return tmpDir })
	t.Cleanup(restore)

	if r := contractOffenseSeen("session-X", "search"); r != false {
		t.Fatalf("first call (session-X, search): expected false, got %v", r)
	}
	sentinel := filepath.Join(tmpDir, ".browzer-guard", "session-X", "contract-search")
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel file missing after first call: %v", err)
	}
	// Legacy JSON ledger MUST NOT be written by the fast path.
	legacy := filepath.Join(tmpDir, ".browzer-guard", "session-X.contract.json")
	if _, err := os.Stat(legacy); err == nil {
		t.Fatalf("unexpected JSON ledger at %s", legacy)
	}
	if r := contractOffenseSeen("session-X", "search"); r != true {
		t.Fatalf("repeat call (session-X, search): expected true, got %v", r)
	}
	// Distinct sub under the same session is its own first-offense.
	if r := contractOffenseSeen("session-X", "deps"); r != false {
		t.Fatalf("first call (session-X, deps): expected false (distinct sub), got %v", r)
	}
}

// TestContractDecide_BarePipeDetected asserts that a bare-pipe invocation such
// as `browzer search foo | head` is recognised as a compound command. The
// regex must split on `|` and identify the browzer segment, triggering the
// missing-flag advisory (ExitAllow + non-empty envelope).
func TestContractDecide_BarePipeDetected(t *testing.T) {
	cleanContractState(t, t.Name())
	raw := makeContractInput(t, "browzer search foo | head", t.Name())
	code, out := contractDecide(raw)

	if code != internalhooks.ExitAllow {
		t.Errorf("expected ExitAllow (%d), got %d", internalhooks.ExitAllow, code)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("expected non-nil envelope signalling missing --save/--json/--schema flag")
	}
	if out.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("expected HookEventName=PreToolUse, got %q", out.HookSpecificOutput.HookEventName)
	}
	// Decision must be "ask" on first offense.
	if out.HookSpecificOutput.PermissionDecision != "ask" {
		t.Errorf("expected PermissionDecision=ask, got %q", out.HookSpecificOutput.PermissionDecision)
	}
}

// TestContractDecide_LogicalORDetected checks that `||` (logical-OR) is also
// recognised so that `browzer search foo || fallback` is treated as compound.
func TestContractDecide_LogicalORDetected(t *testing.T) {
	cleanContractState(t, t.Name())
	raw := makeContractInput(t, "browzer search foo || echo fallback", t.Name())
	code, out := contractDecide(raw)

	if code != internalhooks.ExitAllow {
		t.Errorf("expected ExitAllow (%d), got %d", internalhooks.ExitAllow, code)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("expected non-nil envelope for logical-OR compound command")
	}
}

// TestContractDecide_BrowzerSearchWithSave_PassesThrough asserts that a
// properly-flagged command is not challenged even when piped.
func TestContractDecide_BrowzerSearchWithSave_PassesThrough(t *testing.T) {
	cleanContractState(t, t.Name())
	raw := makeContractInput(t, "browzer search foo --save /tmp/foo.json | head", t.Name())
	code, out := contractDecide(raw)

	// With --save present the guard must pass through.
	if code != internalhooks.ExitPassthrough {
		t.Errorf("expected ExitPassthrough (%d), got %d", internalhooks.ExitPassthrough, code)
	}
	if out != nil {
		t.Errorf("expected nil envelope for compliant command, got %+v", out)
	}
}

// TestContractDecide_PlainBrowzerSearch_NotPiped asserts that a bare browzer
// search without a pipe is also challenged (not piped, but still missing flags).
func TestContractDecide_PlainBrowzerSearch_NotPiped(t *testing.T) {
	cleanContractState(t, t.Name())
	raw := makeContractInput(t, "browzer search foo", t.Name())
	code, _ := contractDecide(raw)

	// Should still get ExitAllow (advisory) on first offense.
	if code != internalhooks.ExitAllow {
		t.Errorf("expected ExitAllow (%d) for missing-flag case, got %d", internalhooks.ExitAllow, code)
	}
}

// TestContractDecide_ShellOperatorRE_MatchesPipe is a unit test directly
// against the compiled regex to guard against future regex regressions.
func TestContractDecide_ShellOperatorRE_MatchesPipe(t *testing.T) {
	cases := []struct {
		input   string
		wantHit bool
		desc    string
	}{
		{"browzer search foo | head", true, "bare pipe"},
		{"browzer search foo || fallback", true, "logical-OR"},
		{"browzer search foo && bar", true, "logical-AND"},
		{"browzer search foo; bar", true, "semicolon"},
		{"browzer search foo\nbar", true, "newline"},
		{"browzer search foo", false, "plain — no operator"},
	}

	for _, c := range cases {
		got := contractHasShellOperatorRE.MatchString(c.input)
		if got != c.wantHit {
			t.Errorf("contractHasShellOperatorRE.MatchString(%q) = %v, want %v (%s)",
				c.input, got, c.wantHit, c.desc)
		}
	}
}
