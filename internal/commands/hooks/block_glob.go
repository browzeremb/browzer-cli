package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// blockGlobMessage is the advisory text emitted in both soft and block modes.
const blockGlobMessage = "Glob bypasses the workspace index. " +
	"Prefer `browzer explore \"<query>\" --json --save /tmp/explore.json` for ranked, deduped, symbol-aware results. " +
	"Use Glob only when looking for files the index cannot know about (newly created, scaffolded, or out-of-tree)."

// blockGlobInput is the PreToolUse(Glob) hook envelope.
type blockGlobInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
		Type    string `json:"type"`
		Include string `json:"include"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
}

// blockGlobDenyEnvelope is the output envelope for PreToolUse denials.
type blockGlobDenyEnvelope struct {
	HookSpecificOutput *blockGlobSpecific `json:"hookSpecificOutput,omitempty"`
}

type blockGlobSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// newBlockGlobCommand returns the cobra command for the block-glob guard.
func newBlockGlobCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "block-glob",
		Short: "Block glob patterns that match sensitive paths",
		RunE:  runBlockGlob,
	}
}

// runBlockGlob is the cobra RunE for the block-glob guard.
func runBlockGlob(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook block-glob: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, out := blockGlobDecide(raw)
	if out != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		_ = enc.SetEscapeHTML
		_ = enc.Encode(out)
	}
	os.Exit(code)
	return nil // unreachable
}

// blockGlobDecide is the testable core. Returns exit code and optional envelope.
func blockGlobDecide(raw []byte) (int, *blockGlobDenyEnvelope) {
	if !internalhooks.GateAllowed("block-glob") {
		return internalhooks.ExitPassthrough, nil
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return internalhooks.ExitPassthrough, nil
	}

	var in blockGlobInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough, nil
	}
	if in.ToolName != "Glob" {
		return internalhooks.ExitPassthrough, nil
	}

	// Build the config-surface target string and skip when matched.
	target := internalhooks.ConfigSurfaceTarget(
		in.ToolInput.Path, in.ToolInput.Pattern, in.ToolInput.Glob,
		in.ToolInput.Type, in.ToolInput.Include,
	)
	if internalhooks.ConfigSurfaceRE.MatchString(target) {
		return internalhooks.ExitPassthrough, nil
	}

	wsRoot := internalhooks.WorkspaceRootFor("")
	mode := internalhooks.WorkspaceGlobMode(wsRoot)

	if mode == "block" {
		// Canonical PreToolUse deny path: exit 0 with hookSpecificOutput containing
		// permissionDecision:"deny". Claude Code reads the stdout JSON envelope and
		// surfaces permissionDecisionReason to the agent. Using exit 0 is the
		// documented hook contract — exit 2 causes Claude Code to discard stdout.
		out := &blockGlobDenyEnvelope{
			HookSpecificOutput: &blockGlobSpecific{
				HookEventName:            "PreToolUse",
				PermissionDecision:       "deny",
				PermissionDecisionReason: blockGlobMessage,
				AdditionalContext:        blockGlobMessage,
			},
		}
		return internalhooks.ExitAllow, out
	}

	// Soft mode: once-per-session advisory.
	if !internalhooks.SessionBannerEmittedOnce(".browzer-block-glob-banner") {
		return internalhooks.ExitPassthrough, nil
	}

	out := &blockGlobDenyEnvelope{
		HookSpecificOutput: &blockGlobSpecific{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			AdditionalContext:  blockGlobMessage + " (This advisory emits once per session.)",
		},
	}
	return internalhooks.ExitAllow, out
}
