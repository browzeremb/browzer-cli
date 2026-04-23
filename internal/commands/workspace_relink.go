package commands

import (
	"fmt"

	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

// registerWorkspaceRelink wires `browzer workspace relink <id>`.
//
// relink points the current directory at an EXISTING workspace on
// the server by rewriting .browzer/config.json. It does NOT create,
// delete, or re-index anything. This is the canonical "I moved the
// repo" / "I want to switch which workspace this checkout tracks"
// operation — it replaces the old `browzer init --force` hack,
// which conflated re-link with re-index and had confusing plan-slot
// accounting.
//
// If the target id does not exist or does not belong to the caller's
// organization, the server's `GetWorkspace` returns nil and we
// refuse to write the config file. No 404 probing oracle: the error
// is "Workspace <id>" not-found, same as `workspace delete`.
func registerWorkspaceRelink(ws *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "relink <id>",
		Short: "Bind current dir to existing workspace",
		Args:  cobra.ExactArgs(1),
		Long: `Point the current directory at an existing Browzer workspace.

Rewrites .browzer/config.json to bind this directory to the given
workspace id. Does NOT create, delete, or re-index anything. Use
when you've moved the repo, re-cloned it, or want to track a
different workspace from the same checkout.

If .browzer/config.json already exists it will be overwritten — use
` + "`browzer workspace unlink`" + ` first if you want the previous binding
audit-visible.

Agent-friendly:
  Pair with ` + "`browzer workspace list --json`" + ` to discover the
  target id first. Relink is the mirror of unlink: unlink clears the
  binding (keeping server data); relink points at an existing
  workspace (still keeping server data). Neither creates or deletes.

Examples:
  browzer workspace relink ws-abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			gitRoot, err := requireGitRoot()
			if err != nil {
				return err
			}

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}

			// Verify the workspace exists and belongs to the caller
			// BEFORE touching the local config. This prevents
			// half-applied relinks where we clobber config.json and
			// THEN discover the id was wrong.
			target, err := ac.Client.GetWorkspace(rootContext(cmd), id)
			if err != nil {
				return err
			}
			if target == nil {
				return cliErrors.NotFound("Workspace " + id)
			}

			if err := config.SaveProjectConfig(gitRoot, &config.ProjectConfig{
				Version:       config.ProjectConfigVersion,
				WorkspaceID:   target.ID,
				WorkspaceName: target.Name,
				Server:        ac.Credentials.Server,
			}); err != nil {
				return err
			}

			ui.Success(fmt.Sprintf("Relinked %s to workspace %s (%q)", gitRoot, target.ID, target.Name))
			return nil
		},
	}
	ws.AddCommand(cmd)
}
