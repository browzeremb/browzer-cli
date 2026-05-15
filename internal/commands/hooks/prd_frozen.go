package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// prdFrozenAmendmentsRE matches the PRD_AMENDMENTS.md exception path.
var prdFrozenAmendmentsRE = regexp.MustCompile(`(^|/)staging/PRD_AMENDMENTS\.md$`)

// prdFrozenPathRE matches the staging/PRD.md path (and only that file).
var prdFrozenPathRE = regexp.MustCompile(`(^|/)staging/PRD\.md$`)

// prdFrozenInput is the PreToolUse(Edit|Write|MultiEdit|NotebookEdit) hook envelope.
// NotebookEdit uses "notebook_path" instead of "file_path"; both fields are
// captured so the guard can locate the target regardless of tool.
type prdFrozenInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// newPrdFrozenCommand returns the cobra command for the prd-frozen guard.
func newPrdFrozenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prd-frozen",
		Short: "Prevent edits to frozen PRD documents",
		RunE:  runPrdFrozen,
	}
}

// runPrdFrozen is the cobra RunE for the prd-frozen guard.
func runPrdFrozen(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook prd-frozen: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code := prdFrozenDecide(raw)
	os.Exit(code)
	return nil // unreachable
}

// prdFrozenDecide is the testable core.
//
//   - exit 1 (ExitPassthrough) — path does not match PRD.md, or no frozen marker.
//   - exit 2 (ExitDeny)        — PRD.md path matched and .prd-frozen sentinel found.
func prdFrozenDecide(raw []byte) int {
	var in prdFrozenInput
	// Malformed payloads must never block a tool call.
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough
	}

	switch in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		// continue to path check below
	default:
		return internalhooks.ExitPassthrough
	}

	// NotebookEdit carries the target in "notebook_path"; all other matched
	// tools use "file_path".
	filePath := in.ToolInput.FilePath
	if filePath == "" {
		filePath = in.ToolInput.NotebookPath
	}
	if filePath == "" {
		return internalhooks.ExitPassthrough
	}

	// Unconditionally allow PRD_AMENDMENTS.md regardless of marker presence.
	if prdFrozenAmendmentsRE.MatchString(filePath) {
		return internalhooks.ExitPassthrough
	}

	// Only inspect the marker when the path is exactly staging/PRD.md.
	if !prdFrozenPathRE.MatchString(filePath) {
		return internalhooks.ExitPassthrough
	}

	marker := filepath.Join(filepath.Dir(filePath), ".prd-frozen")
	if _, err := os.Stat(marker); err == nil {
		fmt.Fprintf(os.Stderr,
			"prd-frozen: PRD.md is frozen — append to staging/PRD_AMENDMENTS.md instead. "+
				"Add findings, AC clarifications, and post-merge wording fixes there. "+
				"The prdSha hash concatenates both files.\n",
		)
		return internalhooks.ExitDeny
	}

	return internalhooks.ExitPassthrough
}
