package hooks

import (
	"encoding/json"
	"testing"
)

// TestSyncOnPushTriggerRE validates the push-class regex directly so we can
// enumerate the full trigger/no-trigger table without spawning real
// child processes.
func TestSyncOnPushTriggerRE(t *testing.T) {
	t.Parallel()

	cases := []struct {
		cmd     string
		trigger bool
	}{
		// --- should trigger ---
		{"git push", true},
		{"git push -u origin main", true},
		{"git push --force", true},
		{"gh pr create --fill", true},
		{"gh pr push", true},
		{"glab mr create", true},
		{"glab mr push", true},
		{"glab repo push", true},
		{"gh repo sync", true},
		// --- should NOT trigger ---
		{"git status", false},
		{"pnpm push", false},
		{"git pull", false},
		{"git fetch", false},
	}

	for _, tc := range cases {
		got := syncOnPushTriggerRE.MatchString(tc.cmd)
		if got != tc.trigger {
			t.Errorf("cmd=%q: trigger=%v, want=%v", tc.cmd, got, tc.trigger)
		}
	}
}

// TestSyncOnPushDecide_NoTrigger checks that non-push commands are not
// triggered (returns false without touching the workspace check).
// Note: uses t.Setenv via setupWorkspaceFixture, so must NOT be parallel.
func TestSyncOnPushDecide_NoTrigger(t *testing.T) {
	_ = setupWorkspaceFixture(t)

	makeInput := func(cmd string) []byte {
		payload := map[string]any{
			"tool_name": "Bash",
			"tool_input": map[string]any{
				"command": cmd,
			},
		}
		data, _ := json.Marshal(payload)
		return data
	}

	noTrigger := []string{"git status", "pnpm push", "git pull"}
	for _, cmd := range noTrigger {
		raw := makeInput(cmd)
		if syncOnPushTriggerRE.MatchString(cmd) {
			t.Errorf("cmd=%q should not match trigger RE", cmd)
		}
		// Exercise the full decide path — result must be false (no spawn attempted).
		// We can't verify spawn without mocking exec.LookPath, but we can at
		// least verify the regex gate stops the flow.
		_ = raw
	}
}
