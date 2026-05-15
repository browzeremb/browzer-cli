package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/diagnostic"
	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// subagentFallbackAllowlist is the hardcoded allowlist used when the
// agents/ directory is absent or empty. Mirrors the JS source exactly.
var subagentFallbackAllowlist = []string{
	"browzer:code-reviewer",
	"browzer:coder",
	"browzer:doc-writer",
	"browzer:explorer",
	"browzer:fixer",
	"browzer:pm",
	"browzer:po",
	"browzer:scoper",
	"browzer:tester",
}

// subagentAgentFileRE matches .md and .mdx filenames.
var subagentAgentFileRE = regexp.MustCompile(`\.(md|mdx)$`)

// validateSubagentInput is the PreToolUse(Task) hook envelope.
type validateSubagentInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		SubagentType string `json:"subagent_type"`
	} `json:"tool_input"`
}

// validateSubagentDenyEnvelope is the stdout payload emitted on deny.
// Claude Code discards stdout on exit 2, but the shell wrapper (F-015)
// reads this envelope via $OUTPUT and forwards it as a structured denial.
type validateSubagentDenyEnvelope struct {
	HookSpecificOutput *validateSubagentDenySpecific `json:"hookSpecificOutput,omitempty"`
}

type validateSubagentDenySpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

// newValidateSubagentTypeCommand returns the cobra command for the
// validate-subagent-type guard.
func newValidateSubagentTypeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate-subagent-type",
		Short: "Validate the subagent type field in dispatches",
		RunE:  runValidateSubagentType,
	}
}

// runValidateSubagentType is the cobra RunE.
func runValidateSubagentType(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook validate-subagent-type: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, out := validateSubagentTypeDecide(raw)
	if out != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
	os.Exit(code)
	return nil // unreachable
}

// validateSubagentTypeDecide is the testable core. Returns the exit code and an
// optional deny envelope. The envelope is non-nil only on the deny path — it
// carries the structured hookSpecificOutput that the F-015 shell wrapper
// forwards to Claude Code as a structured denial with rationale.
func validateSubagentTypeDecide(raw []byte) (int, *validateSubagentDenyEnvelope) {
	var in validateSubagentInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough, nil // malformed → pass through
	}
	if in.ToolName != "Task" {
		return internalhooks.ExitPassthrough, nil
	}

	value := in.ToolInput.SubagentType
	allowlist := buildSubagentAllowlist()

	if value != "" && slices.Contains(allowlist, value) {
		return internalhooks.ExitPassthrough, nil
	}

	// Build a human-readable reason with the closest Levenshtein match so the
	// dispatch-prompt author sees an actionable correction in the Claude Code UI.
	reason := buildDenyReason(value, allowlist)
	fmt.Fprintf(os.Stderr, "subagent-type: rejected — %s\n", reason)

	out := &validateSubagentDenyEnvelope{
		HookSpecificOutput: &validateSubagentDenySpecific{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		},
	}
	return internalhooks.ExitDeny, out
}

// buildDenyReason composes the permissionDecisionReason string.
func buildDenyReason(value string, allowlist []string) string {
	allowed := strings.Join(allowlist, ", ")

	suggestions := diagnostic.DidYouMean(value, allowlist)
	if len(suggestions) > 0 {
		return fmt.Sprintf(
			"Unknown subagent_type '%s'. Closest match: '%s'. Allowed: [%s]",
			value, suggestions[0], allowed,
		)
	}
	return fmt.Sprintf(
		"Unknown subagent_type '%s'. Allowed: [%s]",
		value, allowed,
	)
}

// buildSubagentAllowlist discovers the allowed subagent types by reading the
// agents/ directory relative to the plugin root, then falls back to the
// hardcoded list when the directory is absent or produces no entries.
//
// Plugin root resolution:
//  1. CLAUDE_PLUGIN_ROOT env var (set by Claude Code at hook invocation time).
//
// Falls back to subagentFallbackAllowlist when CLAUDE_PLUGIN_ROOT is unset,
// the agents/ directory is absent, or the directory yields no .md/.mdx files.
func buildSubagentAllowlist() []string {
	agentsDir := ""

	if root := os.Getenv("CLAUDE_PLUGIN_ROOT"); root != "" {
		candidate := filepath.Join(root, "agents")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			agentsDir = candidate
		}
	}

	if agentsDir == "" {
		return subagentFallbackAllowlist
	}

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return subagentFallbackAllowlist
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if subagentAgentFileRE.MatchString(e.Name()) {
			ext := filepath.Ext(e.Name())
			base := strings.TrimSuffix(e.Name(), ext)
			names = append(names, "browzer:"+base)
		}
	}

	if len(names) == 0 {
		return subagentFallbackAllowlist
	}
	return names
}
