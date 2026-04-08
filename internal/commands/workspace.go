package commands

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

func registerWorkspace(parent *cobra.Command) *cobra.Command {
	ws := &cobra.Command{
		Use:   "workspace",
		Short: "Manage Browzer workspaces",
	}

	// list
	var listFilter string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List workspaces in the caller organization",
		Long: `List workspaces in the caller organization.

The optional --filter flag does a case-insensitive substring match
against each workspace's name AND id, which is much friendlier than
piping the full list through grep — especially in orgs with hundreds
of workspaces.

Examples:
  browzer workspace list
  browzer workspace list --filter rag
  browzer workspace list --json
  browzer workspace list --save ws.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			ws, err := ac.Client.ListWorkspaces(rootContext(cmd))
			if err != nil {
				return err
			}
			// Apply --filter (case-insensitive substring on name OR id).
			// Done client-side so the API surface stays unchanged and
			// the same flag works against any backend version.
			if needle := strings.ToLower(strings.TrimSpace(listFilter)); needle != "" {
				filtered := ws[:0]
				for _, w := range ws {
					if strings.Contains(strings.ToLower(w.Name), needle) ||
						strings.Contains(strings.ToLower(w.ID), needle) {
						filtered = append(filtered, w)
					}
				}
				ws = filtered
			}
			// Human-readable rendering uses a lipgloss table; JSON
			// path is untouched so agents keep parsing the same shape.
			human := "No workspaces.\n"
			if len(ws) > 0 {
				rows := make([][]string, len(ws))
				for i, w := range ws {
					rows[i] = []string{
						w.ID, w.Name,
						strconv.Itoa(w.FileCount),
						strconv.Itoa(w.FolderCount),
						strconv.Itoa(w.SymbolCount),
						w.RootPath,
					}
				}
				human = ui.Table(
					[]string{"ID", "Name", "Files", "Folders", "Symbols", "Root"},
					rows,
				)
			}
			return emitOrFail(ws, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	listCmd.Flags().StringVar(&listFilter, "filter", "", "Substring match (case-insensitive) on name or id")
	listCmd.Flags().Bool("json", false, "Emit JSON instead of plain text")
	listCmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	ws.AddCommand(listCmd)

	// get
	getCmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Fetch a single workspace by id (schema-discovery helper)",
		Args:  cobra.ExactArgs(1),
		Long: `Fetch a single workspace by id (schema-discovery helper).

Returns the full workspace DTO so SKILLs can discover the shape
before calling explore/search. Always emits JSON.

Examples:
  browzer workspace get ws-123 --save ws.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			ws, err := ac.Client.GetWorkspace(rootContext(cmd), args[0])
			if err != nil {
				return err
			}
			if ws == nil {
				return cliErrors.NotFound("Workspace " + args[0])
			}
			// Always JSON. Pretty-print to stdout when no --save.
			if saveFlag != "" {
				return emitOrFail(ws, output.Options{JSON: true, Save: saveFlag}, "")
			}
			data, err := json.MarshalIndent(ws, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	}
	getCmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout")
	ws.AddCommand(getCmd)

	// delete
	var confirmName string
	deleteCmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a workspace and all its data",
		Args:  cobra.ExactArgs(1),
		Long: `Delete a workspace and all its data.

Examples:
  browzer workspace delete ws-123 --confirm-name my-repo
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			target, err := ac.Client.GetWorkspace(rootContext(cmd), id)
			if err != nil {
				return err
			}
			if target == nil {
				return cliErrors.NotFound("Workspace " + id)
			}
			confirm := confirmName
			if confirm == "" {
				if !isTTY() {
					return cliErrors.New("Workspace name confirmation required (use --confirm-name in non-interactive shells).")
				}
				_ = huh.NewInput().
					Title(fmt.Sprintf("Type the workspace name (%s) to confirm:", target.Name)).
					Value(&confirm).
					Run()
			}
			if confirm != target.Name {
				return cliErrors.Newf("Workspace name confirmation mismatch (expected %q).", target.Name)
			}
			if err := ac.Client.DeleteWorkspace(rootContext(cmd), id); err != nil {
				return err
			}
			ui.Success(fmt.Sprintf("Deleted workspace %s", id))
			return nil
		},
	}
	deleteCmd.Flags().StringVar(&confirmName, "confirm-name", "", "Skip the interactive prompt by passing the workspace name (for non-interactive use)")
	ws.AddCommand(deleteCmd)

	parent.AddCommand(ws)
	return ws
}
