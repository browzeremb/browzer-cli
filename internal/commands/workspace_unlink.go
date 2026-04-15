package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// registerWorkspaceUnlink wires `browzer workspace unlink`.
//
// unlink disconnects the current directory from its Browzer
// workspace by removing .browzer/config.json locally. The workspace
// on the server is NOT touched — it keeps all its indexed data and
// keeps consuming a slot of the caller's plan. This is the point of
// this command: it exists for the narrow case where the user wants
// to stop syncing from THIS directory (e.g. migrating the sync loop
// to CI) while keeping the workspace live on the server.
//
// For the common "I want this workspace gone" case, use
// `browzer workspace delete <id>` instead — unlink intentionally
// does NOT free the plan slot.
func registerWorkspaceUnlink(ws *cobra.Command) {
	var yes bool
	cmd := &cobra.Command{
		Use:   "unlink",
		Short: "Disconnect the current directory from its Browzer workspace (keeps server data)",
		Long: `Disconnect the current directory from its Browzer workspace.

Removes .browzer/config.json locally. The workspace on the server is
NOT deleted — it keeps its indexed data and continues consuming a
slot of your plan. If you want to free the slot, run
` + "`browzer workspace delete <id>`" + ` instead.

Agent-friendly:
  Use --yes in non-TTY shells. To rebind this directory to an
  existing workspace id, use ` + "`browzer workspace relink <id>`" + ` —
  unlink and relink are the pair that replaces the old
  ` + "`browzer init --force`" + ` hack.

Examples:
  browzer workspace unlink
  browzer workspace unlink --yes
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			gitRoot, err := requireGitRoot()
			if err != nil {
				return err
			}
			existing, err := config.LoadProjectConfig(gitRoot)
			if err != nil {
				return err
			}
			if existing == nil {
				return cliErrors.NoProject()
			}

			// Always surface the plan-slot consequence before the
			// user commits, even on --yes. The warning is the whole
			// point of this command existing; hiding it would defeat
			// the UX rationale for unlink vs delete.
			ui.Warn(fmt.Sprintf(
				"Workspace %s (%q) will remain on the server and continue consuming 1 slot of your plan.",
				existing.WorkspaceID, existing.WorkspaceName,
			))
			ui.Arrow("To free the slot, run: browzer workspace delete " + existing.WorkspaceID)

			if !yes {
				if !isTTY() {
					return cliErrors.New("Refusing to unlink in a non-interactive shell without --yes.")
				}
				confirm := false
				if err := huh.NewConfirm().
					Title("Proceed with unlink?").
					Affirmative("Unlink").
					Negative("Cancel").
					Value(&confirm).
					Run(); err != nil {
					return cliErrors.Newf("Confirmation prompt failed: %v", err)
				}
				if !confirm {
					return cliErrors.New("Aborted.")
				}
			}

			cfgPath := filepath.Join(gitRoot, ".browzer", "config.json")
			if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
				return cliErrors.Newf("Failed to remove %s: %v", cfgPath, err)
			}

			ui.Success(fmt.Sprintf("Unlinked workspace %s from %s", existing.WorkspaceID, gitRoot))
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the interactive confirmation prompt")
	ws.AddCommand(cmd)
}
