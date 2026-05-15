package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// suggestGrepAdvisory is the once-per-session banner text.
const suggestGrepAdvisory = "This repo is indexed by Browzer — Grep bypasses the hybrid vector + Graph RAG. " +
	"Prefer `browzer explore \"<query>\" --json --save /tmp/explore.json` for the same intent: " +
	"returns ranked file entries with exports/imports/importedBy/lines/score in a single call. " +
	"Use Grep only when explore returns nothing useful for the specific string match. " +
	"(This advisory emits once per session.)"

// suggestGrepInput is the PreToolUse(Grep) hook envelope.
type suggestGrepInput struct {
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

// suggestGrepEnvelope is the PreToolUse output envelope (allow + advisory).
type suggestGrepEnvelope struct {
	HookSpecificOutput *suggestGrepSpecific `json:"hookSpecificOutput,omitempty"`
}

type suggestGrepSpecific struct {
	HookEventName      string `json:"hookEventName"`
	PermissionDecision string `json:"permissionDecision"`
	AdditionalContext  string `json:"additionalContext,omitempty"`
}

// newSuggestGrepCommand returns the cobra command for the suggest-grep guard.
func newSuggestGrepCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "suggest-grep",
		Short: "Suggest grep alternatives when glob is wasteful",
		RunE:  runSuggestGrep,
	}
}

// runSuggestGrep is the cobra RunE for the suggest-grep guard.
func runSuggestGrep(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook suggest-grep: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, out := suggestGrepDecide(raw)
	if out != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
	os.Exit(code)
	return nil // unreachable
}

// suggestGrepDecide is the testable core.
func suggestGrepDecide(raw []byte) (int, *suggestGrepEnvelope) {
	if !internalhooks.GateAllowed("suggest-grep") {
		return internalhooks.ExitPassthrough, nil
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return internalhooks.ExitPassthrough, nil
	}

	var in suggestGrepInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough, nil
	}
	if in.ToolName != "Grep" {
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

	// Once-per-session dedup — suppress on repeat.
	if !internalhooks.SessionBannerEmittedOnce(".browzer-suggest-grep-banner") {
		return internalhooks.ExitPassthrough, nil
	}

	out := &suggestGrepEnvelope{
		HookSpecificOutput: &suggestGrepSpecific{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			AdditionalContext:  suggestGrepAdvisory,
		},
	}
	return internalhooks.ExitAllow, out
}
