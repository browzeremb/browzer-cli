package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// subagentStopInput models the SubagentStop event envelope fields the guard
// reads.  Only the five always-available fields are required by the contract;
// cost-shaped fields are optional and conditionally included.
type subagentStopInput struct {
	SessionID  string  `json:"session_id"`
	AgentID    string  `json:"agent_id"`
	AgentType  string  `json:"agent_type"`
	Cwd        string  `json:"cwd"`
	DurationMs float64 `json:"duration_ms"`
	// camelCase aliases (some harness builds emit these).
	DurationMsCamel   float64 `json:"durationMs"`
	ToolUseCount      float64 `json:"tool_use_count"`
	ToolUseCountCamel float64 `json:"toolUseCount"`
	InputTokens       float64 `json:"input_tokens"`
	InputTokensCamel  float64 `json:"inputTokens"`
	OutputTokens      float64 `json:"output_tokens"`
	OutputTokensCamel float64 `json:"outputTokens"`
}

// subagentStopDataDir returns the telemetry directory for subagent-stop JSONL
// files. Prefers $CLAUDE_PLUGIN_DATA/telemetry when set, otherwise
// $HOME/.browzer/telemetry — mirroring the JS guard's layout exactly.
func subagentStopDataDir() string {
	if d := os.Getenv("CLAUDE_PLUGIN_DATA"); d != "" {
		return filepath.Join(d, "telemetry")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer", "telemetry")
}

// newSubagentStopCommand returns the cobra command for the subagent-stop guard.
func newSubagentStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "subagent-stop",
		Short: "Guard fired when a subagent stops",
		RunE:  runSubagentStop,
	}
}

// runSubagentStop is the cobra RunE for the subagent-stop guard.
// Always exits ExitAllow (0) — telemetry must never block the agent loop.
func runSubagentStop(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook subagent-stop: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitAllow)
	}
	subagentStopDecide(raw)
	os.Exit(internalhooks.ExitAllow)
	return nil // unreachable
}

// subagentStopDecide is the testable core. It appends one JSONL record per
// subagent termination to $TELEMETRY_DIR/subagent-YYYY-MM-DD.jsonl.
//
// Schema contract (matches the JS guard):
//   - Five always-available fields (ts, sessionId, agentId, agentType, cwd)
//     use "" fallback when the harness omitted them — schema stays fixed-shape.
//   - Optional numeric fields (durationMs, toolUseCount, inputTokens,
//     outputTokens) are only included when the harness provided a finite value.
//
// Returns the absolute path written on success, "" on skip/error.
func subagentStopDecide(raw []byte) string {
	if !internalhooks.GateAllowed("subagent-stop-telemetry") {
		return ""
	}

	var in subagentStopInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}

	dir := subagentStopDataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}

	today := time.Now().UTC().Format("2006-01-02")
	file := filepath.Join(dir, "subagent-"+today+".jsonl")

	// Always-available fields — empty-string fallback preserves schema stability.
	record := map[string]any{
		"ts":        time.Now().UTC().Format(time.RFC3339Nano),
		"sessionId": in.SessionID,
		"agentId":   in.AgentID,
		"agentType": in.AgentType,
		"cwd":       in.Cwd,
	}

	// Optional numeric fields — only include when the harness provided a
	// finite value (mirrors the JS guard's `Number.isFinite` gate).
	addFinite := func(key string, snake, camel float64) {
		v := snake
		if v == 0 {
			v = camel
		}
		if v != 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
			record[key] = v
		}
	}
	addFinite("durationMs", in.DurationMs, in.DurationMsCamel)
	addFinite("toolUseCount", in.ToolUseCount, in.ToolUseCountCamel)
	addFinite("inputTokens", in.InputTokens, in.InputTokensCamel)
	addFinite("outputTokens", in.OutputTokens, in.OutputTokensCamel)

	line, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	line = append(line, '\n')

	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return ""
	}
	return file
}
