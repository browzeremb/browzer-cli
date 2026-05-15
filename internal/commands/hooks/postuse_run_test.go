package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// makePostuseRunInput builds the raw JSON for a PostToolUse(Bash) event.
// stdoutJSON is the raw JSON string to embed as tool_response.stdout.
func makePostuseRunInput(command, stdoutJSON string) []byte {
	type respObj struct {
		Stdout string `json:"stdout"`
	}
	type toolInput struct {
		Command string `json:"command"`
	}
	type env struct {
		ToolName     string   `json:"tool_name"`
		ToolInput    toolInput `json:"tool_input"`
		ToolResponse respObj  `json:"tool_response"`
	}
	v := env{
		ToolName:     "Bash",
		ToolInput:    toolInput{Command: command},
		ToolResponse: respObj{Stdout: stdoutJSON},
	}
	b, _ := json.Marshal(v)
	return b
}

// make15EntryJSON returns a JSON string with 15 browzer-explore-style entries.
// Entries are given distinct scores so sorting is deterministic.
func make15EntryJSON() string {
	type entry struct {
		Path  string  `json:"path"`
		Score float64 `json:"score"`
	}
	entries := make([]entry, 15)
	for i := range entries {
		entries[i] = entry{
			Path:  fmt.Sprintf("packages/core/src/file%02d.ts", i+1),
			Score: float64(15-i) * 0.05,
		}
	}
	b, _ := json.Marshal(entries)
	return string(b)
}

// make5EntryJSON returns a JSON string with exactly 5 entries.
func make5EntryJSON() string {
	type entry struct {
		Path  string  `json:"path"`
		Score float64 `json:"score"`
	}
	entries := make([]entry, 5)
	for i := range entries {
		entries[i] = entry{
			Path:  fmt.Sprintf("packages/core/src/small%02d.ts", i+1),
			Score: float64(5-i) * 0.1,
		}
	}
	b, _ := json.Marshal(entries)
	return string(b)
}

// TestPostuseRun_15Entries_ReturnsTop5 asserts that a 15-entry browzer
// explore response exits 0 and injects additionalContext naming the top 5.
func TestPostuseRun_15Entries_ReturnsTop5(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makePostuseRunInput("browzer explore foo --json", make15EntryJSON())
	code, out := postuseRunDecide(raw)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("expected non-nil hookSpecificOutput")
	}
	ctx := out.HookSpecificOutput.AdditionalContext
	if out.HookSpecificOutput.HookEventName != "PostToolUse" {
		t.Errorf("hookEventName = %q, want PostToolUse", out.HookSpecificOutput.HookEventName)
	}

	// Must mention the top-5 (highest-score) entries.
	for i := 1; i <= 5; i++ {
		want := fmt.Sprintf("packages/core/src/file%02d.ts", i)
		if !strings.Contains(ctx, want) {
			t.Errorf("additionalContext missing top entry %q\ngot:\n%s", want, ctx)
		}
	}

	// Must NOT mention entry #6 through #15 (outside top-5).
	for i := 6; i <= 15; i++ {
		notWant := fmt.Sprintf("file%02d.ts", i)
		if strings.Contains(ctx, notWant) {
			t.Errorf("additionalContext unexpectedly contains non-top entry %q", notWant)
		}
	}
}

// TestPostuseRun_5Entries_Passthrough asserts that ≤10 entries yields exit 1.
func TestPostuseRun_5Entries_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makePostuseRunInput("browzer explore foo --json", make5EntryJSON())
	code, out := postuseRunDecide(raw)

	if code != 1 {
		t.Fatalf("expected exit 1 (passthrough), got %d", code)
	}
	if out != nil {
		t.Error("expected nil output on passthrough path")
	}
}

// TestPostuseRun_NonBrowzerCommand_Passthrough asserts that a non-browzer
// bash command is always passthrough.
func TestPostuseRun_NonBrowzerCommand_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makePostuseRunInput("ls -la", make15EntryJSON())
	code, out := postuseRunDecide(raw)

	if code != 1 {
		t.Fatalf("expected exit 1 (passthrough), got %d", code)
	}
	if out != nil {
		t.Error("expected nil output")
	}
}

// TestPostuseRun_MalformedJSON_Passthrough asserts that a non-JSON
// tool_response causes passthrough without panicking.
func TestPostuseRun_MalformedJSON_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makePostuseRunInput("browzer explore foo --json", "not-json-at-all{{{")
	code, out := postuseRunDecide(raw)

	if code != 1 {
		t.Fatalf("expected exit 1 (passthrough), got %d", code)
	}
	if out != nil {
		t.Error("expected nil output")
	}
}

// TestPostuseRun_SearchCommand_ReturnsTop5 asserts that `browzer search`
// (not only `browzer explore`) also triggers the injection.
func TestPostuseRun_SearchCommand_ReturnsTop5(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makePostuseRunInput("browzer search \"vector retrieval\" --json", make15EntryJSON())
	code, out := postuseRunDecide(raw)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("expected non-nil output")
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "15 entries") {
		t.Errorf("expected entry count in context, got: %s", out.HookSpecificOutput.AdditionalContext)
	}
}

// TestPostuseRun_ExactlyThreshold_Passthrough asserts that exactly 10
// entries (= threshold) yields passthrough (the condition is strictly ">").
func TestPostuseRun_ExactlyThreshold_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)

	type entry struct {
		Path  string  `json:"path"`
		Score float64 `json:"score"`
	}
	entries := make([]entry, 10)
	for i := range entries {
		entries[i] = entry{Path: fmt.Sprintf("f%d.ts", i), Score: float64(i) * 0.1}
	}
	b, _ := json.Marshal(entries)

	raw := makePostuseRunInput("browzer explore foo --json", string(b))
	code, out := postuseRunDecide(raw)

	if code != 1 {
		t.Fatalf("expected exit 1 at threshold, got %d", code)
	}
	if out != nil {
		t.Error("expected nil output at threshold")
	}
}

// TestPostuseRun_WrappedEntriesForm_ReturnsTop5 tests the { entries: [...] }
// wrapper shape returned by some browzer commands.
func TestPostuseRun_WrappedEntriesForm_ReturnsTop5(t *testing.T) {
	setupWorkspaceFixture(t)

	type entry struct {
		Path  string  `json:"path"`
		Score float64 `json:"score"`
	}
	entries := make([]entry, 12)
	for i := range entries {
		entries[i] = entry{Path: fmt.Sprintf("wrapped/file%02d.ts", i+1), Score: float64(12-i) * 0.07}
	}
	wrapped := map[string]any{"entries": entries}
	b, _ := json.Marshal(wrapped)

	raw := makePostuseRunInput("browzer deps packages/core/src/search.ts --json", string(b))
	code, out := postuseRunDecide(raw)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if out == nil || out.HookSpecificOutput == nil {
		t.Fatal("expected non-nil output")
	}
	// Top entry should be wrapped/file01.ts (score 0.84).
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "wrapped/file01.ts") {
		t.Errorf("expected top entry in context, got: %s", out.HookSpecificOutput.AdditionalContext)
	}
}
