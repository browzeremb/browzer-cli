package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// postuseGrepInput is the PostToolUse(Grep) hook envelope.
type postuseGrepInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
		Type    string `json:"type"`
		Include string `json:"include"`
	} `json:"tool_input"`
	ToolResponse struct {
		Content string `json:"content"`
		Stdout  string `json:"stdout"`
	} `json:"tool_response"`
	SessionID string `json:"session_id"`
}

// newPostuseGrepCommand returns the cobra command for the postuse-grep guard.
func newPostuseGrepCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "postuse-grep",
		Short: "Post-tool-use guard for Grep calls",
		RunE:  runPostuseGrep,
	}
}

// runPostuseGrep is the cobra RunE for the postuse-grep guard.
// It fires telemetry measuring actual output bytes and always exits passthrough.
func runPostuseGrep(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook postuse-grep: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	postuseGrepDecide(raw)
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// postuseGrepDecide is the testable core. It fires a fire-and-forget
// telemetry event with the measured output bytes from the Grep call.
// Returns true when telemetry was submitted.
func postuseGrepDecide(raw []byte) bool {
	if !internalhooks.GateAllowed("postuse-grep") {
		return false
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return false
	}

	var in postuseGrepInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}
	if in.ToolName != "Grep" {
		return false
	}

	// Build the config-surface target string and skip telemetry when matched.
	target := internalhooks.ConfigSurfaceTarget(
		in.ToolInput.Path, in.ToolInput.Pattern, in.ToolInput.Glob,
		in.ToolInput.Type, in.ToolInput.Include,
	)
	if internalhooks.ConfigSurfaceRE.MatchString(target) {
		return false
	}

	content := in.ToolResponse.Content
	if content == "" {
		content = in.ToolResponse.Stdout
	}
	outputBytes := len([]byte(content))

	// Estimate saved tokens: browzer explore would have returned a
	// semantically filtered result set; a grep returns raw matches.
	savedTokens := outputBytes / 4

	internalhooks.TrackEvent(map[string]any{
		"ts":               "",
		"source":           "hook-grep-suggested",
		"command":          "Grep",
		"inputBytes":       0,
		"outputBytes":      outputBytes,
		"savedTokens":      savedTokens,
		"savingsPct":       0,
		"filterLevel":      "suggested",
		"filterFailed":     false,
		"execMs":           0,
		"sessionId":        in.SessionID,
		"estimationMethod": "measured",
	})

	return true
}
