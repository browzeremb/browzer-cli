package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// syncOnPushTriggerRE matches push-class commands:
//   - git push
//   - gh pr create, gh pr push, gh repo sync
//   - glab mr create, glab mr push, glab repo push
var syncOnPushTriggerRE = regexp.MustCompile(
	`\b(?:git\s+push|gh\s+(?:pr\s+(?:create|push)|repo\s+sync)|glab\s+(?:mr\s+(?:create|push)|repo\s+push))\b`,
)

// syncOnPushInput is the PostToolUse(Bash) hook envelope.
type syncOnPushInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
}

// newSyncOnPushCommand returns the cobra command for the sync-on-push guard.
func newSyncOnPushCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync-on-push",
		Short: "Trigger workspace sync on git push",
		RunE:  runSyncOnPush,
	}
}

// runSyncOnPush is the cobra RunE for the sync-on-push guard.
func runSyncOnPush(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook sync-on-push: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	triggered := syncOnPushDecide(raw)
	if triggered {
		os.Exit(internalhooks.ExitAllow)
	}
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// syncOnPushDecide is the testable core. Returns true when a sync was triggered.
func syncOnPushDecide(raw []byte) bool {
	if !internalhooks.GateAllowed("sync-on-push") {
		return false
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return false
	}

	var in syncOnPushInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}

	cmdStr := in.ToolInput.Command
	if !syncOnPushTriggerRE.MatchString(cmdStr) {
		return false
	}

	browzerBin, err := exec.LookPath("browzer")
	if err != nil {
		return false
	}

	// Fire-and-forget: detached child so the index refresh never blocks the
	// agent loop.
	child := exec.Command(browzerBin, "workspace", "sync")
	internalhooks.ApplyDetachSysProcAttr(child)
	child.Stdout = nil
	child.Stderr = nil
	child.Stdin = nil
	if startErr := child.Start(); startErr == nil {
		_ = child.Process.Release()
	}

	return true
}
