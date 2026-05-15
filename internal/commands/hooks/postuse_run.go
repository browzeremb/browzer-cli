package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// browzerJSONCommandRE matches a bash command whose first meaningful token is
// `browzer` followed by one of the four search verbs. An optional
// BROWZER_VAR=VAL prefix (as injected by the rewrite-bash guard) is tolerated.
// The `--json` flag must appear somewhere in the command.
var browzerJSONCommandRE = regexp.MustCompile(
	`(?i)^\s*(?:[A-Z_][A-Z0-9_]*=\S+\s+)*browzer\s+(explore|search|deps|ask)\b.*--json\b`,
)

// postuseRunThreshold is the minimum number of entries that triggers the
// top-5 additionalContext injection. At or below this count the model can
// parse the full output without help.
const postuseRunThreshold = 10

// postuseRunTop is the number of entries surfaced in the additionalContext.
const postuseRunTop = 5

// postuseScoredEntry captures the fields the summariser cares about from each
// result entry. Unknown fields are ignored.
type postuseScoredEntry struct {
	Path  string  `json:"path"`
	File  string  `json:"file"`
	URL   string  `json:"url"`
	Score float64 `json:"score"`
}

// postuseBrowzerResponse is the top-level shape of a `browzer --json`
// response. It tries both an `entries` wrapper and a bare array.
type postuseBrowzerResponse struct {
	Entries []json.RawMessage `json:"entries"`
	Data    []json.RawMessage `json:"data"`
}

// postuseRunInput is the PostToolUse hook envelope. tool_response may be
// an object (with a `stdout` field) or a raw string — both are handled.
type postuseRunInput struct {
	ToolName     string         `json:"tool_name"`
	ToolInput    map[string]any `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// postToolUseOutput is the outer envelope written to stdout when the guard
// produces an additionalContext injection (PostToolUse event shape).
type postToolUseOutput struct {
	HookSpecificOutput *postToolUseSpecific `json:"hookSpecificOutput,omitempty"`
}

type postToolUseSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// newPostuseRunCommand returns the cobra command for the postuse-run guard.
func newPostuseRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "postuse-run",
		Short: "Post-tool-use guard for Bash/Run calls",
		RunE:  runPostuseRun,
	}
}

// runPostuseRun is the cobra RunE. It reads stdin, delegates to
// postuseRunDecide for the pure logic, writes any envelope, and exits.
func runPostuseRun(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook postuse-run: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, out := postuseRunDecide(raw)
	if out != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
	os.Exit(code)
	return nil // unreachable
}

// postuseRunDecide is the testable core of the postuse-run guard.
// Returns the exit code and, on the injection path, a non-nil output envelope.
func postuseRunDecide(raw []byte) (int, *postToolUseOutput) {
	if !internalhooks.GateAllowed("postuse-run") {
		return internalhooks.ExitPassthrough, nil
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return internalhooks.ExitPassthrough, nil
	}

	var in postuseRunInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough, nil
	}
	if in.ToolName != "Bash" {
		return internalhooks.ExitPassthrough, nil
	}

	cmdStr, _ := in.ToolInput["command"].(string)
	if !browzerJSONCommandRE.MatchString(cmdStr) {
		return internalhooks.ExitPassthrough, nil
	}

	// Extract stdout from tool_response, which may be an object or a string.
	stdout := extractStdout(in.ToolResponse)
	if stdout == "" {
		return internalhooks.ExitPassthrough, nil
	}

	entries, ok := parseBrowzerEntries([]byte(stdout))
	if !ok || len(entries) <= postuseRunThreshold {
		return internalhooks.ExitPassthrough, nil
	}

	// Sort by score descending, take top postuseRunTop.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Score > entries[j].Score
	})
	top := entries
	if len(top) > postuseRunTop {
		top = top[:postuseRunTop]
	}

	// Format the summary lines.
	lines := fmt.Sprintf("[browzer] %d entries returned. Top %d:\n", len(entries), len(top))
	for i, e := range top {
		p := e.Path
		if p == "" {
			p = e.URL
		}
		if p == "" {
			p = e.File
		}
		if p == "" {
			p = "?"
		}
		score := ""
		if e.Score != 0 {
			score = fmt.Sprintf(" (%.3f)", e.Score)
		}
		lines += fmt.Sprintf("  %d. %s%s\n", i+1, p, score)
	}
	lines += "  … full results in --save file or stdout above."

	return internalhooks.ExitAllow, &postToolUseOutput{
		HookSpecificOutput: &postToolUseSpecific{
			HookEventName:     "PostToolUse",
			AdditionalContext: lines,
		},
	}
}

// extractStdout returns the stdout string from a raw tool_response JSON value.
// The response may be a JSON object with a `stdout` or `content` key, or it
// may be a bare JSON string — both are handled.
func extractStdout(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try object form first.
	var obj struct {
		Stdout  string `json:"stdout"`
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.Stdout != "" {
			return obj.Stdout
		}
		if obj.Content != "" {
			return obj.Content
		}
	}

	// Try bare string form.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	return ""
}

// parseBrowzerEntries parses the stdout JSON from a `browzer --json` call into
// a slice of scored entries. Returns (nil, false) on any parse failure.
func parseBrowzerEntries(data []byte) ([]postuseScoredEntry, bool) {
	if len(data) == 0 {
		return nil, false
	}

	// Attempt wrapped form: { entries: [...] } or { data: [...] }.
	var wrapped postuseBrowzerResponse
	if json.Unmarshal(data, &wrapped) == nil {
		raw := wrapped.Entries
		if len(raw) == 0 {
			raw = wrapped.Data
		}
		if len(raw) > 0 {
			return decodeEntries(raw)
		}
	}

	// Attempt bare array form.
	var rawArr []json.RawMessage
	if json.Unmarshal(data, &rawArr) == nil && len(rawArr) > 0 {
		return decodeEntries(rawArr)
	}

	return nil, false
}

// decodeEntries decodes a slice of raw JSON messages into postuseScoredEntry
// values. Entries that fail to decode are skipped (best-effort).
func decodeEntries(raws []json.RawMessage) ([]postuseScoredEntry, bool) {
	out := make([]postuseScoredEntry, 0, len(raws))
	for _, r := range raws {
		var e postuseScoredEntry
		if json.Unmarshal(r, &e) == nil {
			out = append(out, e)
		}
	}
	return out, len(out) > 0
}
