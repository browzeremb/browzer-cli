package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// indexableExts is the set of file extensions whose edits should trigger an
// incremental workspace sync. Mirrors the INDEXABLE_EXT set in the JS source.
var indexableExts = map[string]bool{
	".md":    true,
	".mdx":   true,
	".pdf":   true,
	".txt":   true,
	".ts":    true,
	".tsx":   true,
	".js":    true,
	".jsx":   true,
	".mjs":   true,
	".cjs":   true,
	".go":    true,
	".py":    true,
	".rs":    true,
	".java":  true,
	".kt":    true,
	".swift": true,
	".rb":    true,
	".php":   true,
}

// incrementalSyncInput is the PostToolUse(Edit|Write) hook envelope.
type incrementalSyncInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
}

// newIncrementalSyncCommand returns the cobra command for the incremental-sync guard.
func newIncrementalSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "incremental-sync",
		Short: "Incremental workspace sync guard",
		RunE:  runIncrementalSync,
	}
}

// runIncrementalSync is the cobra RunE for the incremental-sync guard.
// Always exits ExitPassthrough — purely fire-and-forget observational.
func runIncrementalSync(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook incremental-sync: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	incrementalSyncDecide(raw)
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// incrementalSyncDecide is the testable core. Returns true when a sync was
// kicked off, false otherwise.
func incrementalSyncDecide(raw []byte) bool {
	if !internalhooks.GateAllowed("incremental-sync") {
		return false
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return false
	}

	var in incrementalSyncInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}
	if in.ToolName != "Edit" && in.ToolName != "Write" {
		return false
	}

	filePath := in.ToolInput.FilePath
	if filePath == "" {

	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if !indexableExts[ext] {
		return false
	}

	browzerBin, err := exec.LookPath("browzer")
	if err != nil {
		return false
	}

	// Fire-and-forget: detached child so the index never blocks the agent loop.
	child := exec.Command(browzerBin, "sync", "--paths", filePath)
	internalhooks.ApplyDetachSysProcAttr(child)
	child.Stdout = nil
	child.Stderr = nil
	child.Stdin = nil
	if startErr := child.Start(); startErr != nil {
		return false
	}
	_ = child.Process.Release()

	// Emit a TrackEvent so the daemon records the delta.
	internalhooks.TrackEvent(map[string]any{
		"ts":               "",
		"source":           "hook-incremental-sync",
		"command":          "incremental-sync",
		"inputBytes":       0,
		"outputBytes":      0,
		"savedTokens":      0,
		"savingsPct":       0,
		"filterLevel":      nil,
		"filterFailed":     false,
		"execMs":           0,
		"sessionId":        in.SessionID,
		"estimationMethod": "measured",
	})

	return true
}
