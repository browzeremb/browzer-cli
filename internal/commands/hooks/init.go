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

// browzerInitRE matches a command that invokes `browzer init`.
var browzerInitRE = regexp.MustCompile(`\bbrowzer\s+init\b`)

// initGuardInput is the PreToolUse(Bash) hook envelope for the init guard.
type initGuardInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	} `json:"tool_input"`
	Cwd string `json:"cwd"`
}

// newInitGuardCommand returns the cobra command for the init guard.
func newInitGuardCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Guard fired at workspace init time",
		RunE:  runInitGuard,
	}
}

// runInitGuard is the cobra RunE for the init guard.
func runInitGuard(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook init: stdin read failed: %v\n", err)
		_, _ = os.Exit, internalhooks.ExitPassthrough
	}
	initGuardDecide(raw)
	os.Exit(internalhooks.ExitPassthrough)
	return nil // unreachable
}

// initGuardDecide is the testable core. Returns true when a warning was emitted.
func initGuardDecide(raw []byte) bool {
	var in initGuardInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}

	cmd := in.ToolInput.Command
	if !browzerInitRE.MatchString(cmd) {
		return false
	}

	// Resolve effective cwd: prefer tool_input.cwd, fall back to top-level cwd.
	cwd := in.ToolInput.Cwd
	if cwd == "" {
		cwd = in.Cwd
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return false
		}
	}

	if _, err := os.Stat(filepath.Join(cwd, ".browzer")); err == nil {
		fmt.Fprintf(os.Stderr,
			"Warning: this directory is already bound to a Browzer workspace (.browzer/ exists in %s). "+
				"Running `browzer init` again will rebind locally — the previous workspace index stays on the server, "+
				"but this directory will point at a new workspace id. "+
				"To inspect the current binding first: `browzer status --json --save /tmp/status.json`. "+
				"To detach without re-init: `browzer workspace unlink`. "+
				"Proceeding — this is a warn, not a block.\n",
			cwd,
		)
		return true
	}
	return false
}
