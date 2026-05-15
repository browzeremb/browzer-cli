package hooks

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// postuseReadLargeThreshold is the minimum file size (bytes) that triggers the
// telemetry event. Files below 40 KB add negligible context-savings signal.
const postuseReadLargeThreshold = 40 * 1024 // 40 960 bytes

// postuseReadInput is the PostToolUse(Read) hook envelope.
type postuseReadInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
}

// newPostuseReadCommand returns the cobra command for the postuse-read guard.
func newPostuseReadCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "postuse-read",
		Short: "Post-tool-use guard for Read calls",
		RunE:  runPostuseRead,
	}
}

// runPostuseRead is the cobra RunE for the postuse-read guard.
// It fires telemetry for large code-file reads and always exits passthrough —
// this guard is observational only (never mutates tool input or output).
func runPostuseRead(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook postuse-read: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	postuseReadDecide(raw)
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// postuseReadDecide is the testable core. It fires a fire-and-forget
// telemetry event for large code-file reads. Returns true when telemetry
// was submitted, false otherwise (passthrough either way).
func postuseReadDecide(raw []byte) bool {
	if !internalhooks.GateAllowed("postuse-read") {
		return false
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return false
	}

	var in postuseReadInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}
	if in.ToolName != "Read" {
		return false
	}

	filePath := in.ToolInput.FilePath
	if filePath == "" {
		return false
	}
	if internalhooks.ClassifyPath(filePath) != internalhooks.ClassCode {
		return false
	}
	if internalhooks.NeverRewriteRE.MatchString(filePath) {
		return false
	}

	absPath := filePath
	if !filepath.IsAbs(filePath) {
		cwd, err := os.Getwd()
		if err != nil {
			return false
		}
		absPath = filepath.Join(cwd, filePath)
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	originalSize := info.Size()
	if originalSize < postuseReadLargeThreshold {
		return false
	}

	// Compute a short hash to correlate events to a specific file without
	// leaking the full path into telemetry.
	h := sha256.Sum256([]byte(absPath))
	pathHash := fmt.Sprintf("%x", h[:6])

	savedTokens := int(originalSize/4) * 2 / 5 // ~0.4 × tokensOf(size)
	wsID := internalhooks.WorkspaceRootFor("")

	internalhooks.TrackEvent(map[string]any{
		"ts":               "",
		"source":           "hook-read",
		"command":          "Read",
		"inputBytes":       0,
		"outputBytes":      0,
		"savedTokens":      savedTokens,
		"savingsPct":       40.0,
		"filterLevel":      "auto",
		"filterFailed":     false,
		"execMs":           0,
		"sessionId":        in.SessionID,
		"pathHash":         pathHash,
		"workspaceId":      wsID,
		"estimationMethod": "estimated",
	})

	return true
}
