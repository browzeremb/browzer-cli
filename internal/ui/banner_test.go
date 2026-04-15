package ui

import (
	"os"
	"testing"
)

// TestColorEnabled_BROWZER_LLM asserts that setting BROWZER_LLM=1 forces
// colorEnabled() to report false regardless of TTY status. This is the
// SKILL-wrapper opt-in path for LLM mode.
func TestColorEnabled_BROWZER_LLM(t *testing.T) {
	t.Setenv("BROWZER_LLM", "1")
	// Clear any existing NO_COLOR interference.
	t.Setenv("NO_COLOR", "")
	_ = os.Unsetenv("NO_COLOR")

	prev := LLMMode
	LLMMode = false
	t.Cleanup(func() { LLMMode = prev })

	if colorEnabled() {
		t.Fatalf("colorEnabled() should be false when BROWZER_LLM is set")
	}
}

// TestColorEnabled_LLMMode asserts the package-level LLMMode var disables
// colors. PersistentPreRunE in commands/root.go flips this when --llm
// arrives on the CLI.
func TestColorEnabled_LLMMode(t *testing.T) {
	_ = os.Unsetenv("BROWZER_LLM")
	_ = os.Unsetenv("NO_COLOR")

	prev := LLMMode
	LLMMode = true
	t.Cleanup(func() { LLMMode = prev })

	if colorEnabled() {
		t.Fatalf("colorEnabled() should be false when LLMMode is true")
	}
}
