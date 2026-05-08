package runfilters

import (
	"strings"
	"testing"
)

const turboAllPass = `• Packages in scope: api, web, worker
• Running build in 3 packages
>>> api#test
 ✓ api#test (2.1s)
>>> web#test
 ✓ web#test (1.8s)
>>> worker#test
 ✓ worker#test (0.9s)

Tasks:  3 successful, 3 total
`

const turboWithFailure = `• Packages in scope: api, web
>>> api#test
 ✓ api#test (1.1s)
>>> web#test

 FAIL src/components/Button.test.ts

  Error: expected "primary" to equal "secondary"

 ✗ web#test (0.5s)

Tasks:  1 successful, 1 failed, 2 total
`

func TestFilterTurbo_AllPass_JustTasksSummary(t *testing.T) {
	out := filterTurbo([]byte(turboAllPass))
	s := string(out)

	// Tasks summary must be kept.
	if !strings.Contains(s, "Tasks:") {
		t.Error("expected 'Tasks:' summary line to be kept")
	}
	if !strings.Contains(s, "3 successful") {
		t.Error("expected '3 successful' in Tasks summary")
	}

	// Success lines should be dropped.
	if strings.Contains(s, "✓ api#test") || strings.Contains(s, "✓ web#test") {
		t.Error("expected ✓ success lines to be dropped")
	}

	// Verbose • header lines should be dropped.
	if strings.Contains(s, "Packages in scope") {
		t.Error("expected '• Packages in scope' line to be dropped")
	}
}

func TestFilterTurbo_Failure_FailureLinesAndErrors(t *testing.T) {
	out := filterTurbo([]byte(turboWithFailure))
	s := string(out)

	// Failure lines (✗) must be kept.
	if !strings.Contains(s, "✗ web#test") {
		t.Error("expected '✗ web#test' failure line to be kept")
	}

	// FAIL lines from nested output must be kept.
	if !strings.Contains(s, "FAIL src/components/Button.test.ts") {
		t.Error("expected 'FAIL src/components/Button.test.ts' to be kept")
	}

	// Error output must be kept.
	if !strings.Contains(s, "Error:") {
		t.Error("expected 'Error:' line to be kept")
	}

	// Summary must be kept.
	if !strings.Contains(s, "Tasks:") {
		t.Error("expected 'Tasks:' summary to be kept")
	}

	// Success lines must be dropped.
	if strings.Contains(s, "✓ api#test") {
		t.Error("expected '✓ api#test' to be dropped")
	}
}

func TestFilterTurbo_SuccessLineDrop_NoDuplicate(t *testing.T) {
	// Regression for FR-02: the tautology bug caused redundant but harmless
	// duplicate checks. Verify that a single ✓ line is correctly dropped.
	input := " ✓ api#test (2.1s)\n" +
		"Tasks:  1 successful, 1 total\n"
	out := filterTurbo([]byte(input))
	s := string(out)

	if strings.Contains(s, "✓ api#test") {
		t.Error("expected ✓ success line to be dropped (FR-02 regression)")
	}
	if !strings.Contains(s, "Tasks:") {
		t.Error("expected Tasks summary to be kept")
	}
}

func TestFilterTurbo_RegistrationKeys(t *testing.T) {
	for _, args := range [][]string{{"pnpm", "turbo"}, {"turbo", "run"}} {
		if fn := DefaultRegistry.Resolve(args); fn == nil {
			t.Errorf("expected filter registered for args %v", args)
		}
	}
}
