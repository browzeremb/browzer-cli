package commands

import (
	"fmt"
	"time"

	"github.com/browzeremb/browzer-cli/internal/auth"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

func registerStatus(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Browzer login and workspace status",
		Long: `Show Browzer login and workspace status.

Examples:
  browzer status
  browzer status --json
  browzer status --json --save status.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			// Surface "Not logged in" before "No Browzer project here"
			// so the user gets the most actionable hint first.
			creds := auth.LoadCredentials()
			if creds == nil {
				return cliErrors.NotAuthenticated()
			}

			gitRoot, err := requireGitRoot()
			if err != nil {
				return err
			}
			project, err := config.LoadProjectConfig(gitRoot)
			if err != nil {
				return err
			}
			if project == nil {
				return cliErrors.NoProject()
			}

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			ws, err := ac.Client.GetWorkspace(rootContext(cmd), project.WorkspaceID)
			if err != nil {
				return err
			}
			if ws == nil {
				return cliErrors.WithCode("Workspace was deleted on server. Run `browzer init --force` to re-create.", 4)
			}

			payload := map[string]any{
				"user":              map[string]string{"id": creds.UserID},
				"organization":      map[string]string{"id": creds.OrganizationID},
				"server":            project.Server,
				"tokenExpiresAt":    creds.ExpiresAt,
				"tokenExpiresHuman": formatExpiry(creds.ExpiresAt),
				"workspace": map[string]any{
					"id":          project.WorkspaceID,
					"name":        project.WorkspaceName,
					"root":        gitRoot,
					"fileCount":   ws.FileCount,
					"folderCount": ws.FolderCount,
					"symbolCount": ws.SymbolCount,
				},
			}

			human := fmt.Sprintf(
				"User:         %s\nOrganization: %s\nServer:       %s\nToken expires: %s\n\nWorkspace:    %s (%s)\nRoot:         %s\nFiles:        %d\nFolders:      %d\nSymbols:      %d\n",
				creds.UserID, creds.OrganizationID, project.Server,
				formatExpiry(creds.ExpiresAt),
				project.WorkspaceName, project.WorkspaceID, gitRoot,
				ws.FileCount, ws.FolderCount, ws.SymbolCount,
			)

			return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON on stdout")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}

// formatExpiry mirrors src/commands/status.ts:formatExpiry — humanises
// an ISO timestamp as "in N days" / "expires today" / "expired".
func formatExpiry(iso string) string {
	if iso == "" {
		return "unknown"
	}
	exp, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		exp, err = time.Parse(time.RFC3339, iso)
		if err != nil {
			return "unknown"
		}
	}
	days := int(time.Until(exp).Hours() / 24)
	if days < 0 {
		return "expired"
	}
	if days == 0 {
		return "expires today"
	}
	if days == 1 {
		return "in 1 day"
	}
	return fmt.Sprintf("in %d days", days)
}
