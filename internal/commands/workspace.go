package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// workspaceListSchemaJSON is the baked-in JSON Schema 2020-12 doc for
// the workspace list response (an array of workspace DTOs).
const workspaceListSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "WorkspaceListResponse",
  "type": "array",
  "items": {
    "type": "object",
    "required": ["id", "name"],
    "properties": {
      "id":          {"type": "string"},
      "name":        {"type": "string"},
      "rootPath":    {"type": "string"},
      "fileCount":   {"type": "integer"},
      "folderCount": {"type": "integer"},
      "symbolCount": {"type": "integer"}
    }
  }
}
`

// workspaceGetSchemaJSON is the baked-in JSON Schema 2020-12 doc for
// the workspace get response (a single workspace DTO).
const workspaceGetSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "WorkspaceGetResponse",
  "type": "object",
  "required": ["id", "name"],
  "properties": {
    "id":          {"type": "string"},
    "name":        {"type": "string"},
    "rootPath":    {"type": "string"},
    "fileCount":   {"type": "integer"},
    "folderCount": {"type": "integer"},
    "symbolCount": {"type": "integer"}
  }
}
`

func registerWorkspace(parent *cobra.Command) *cobra.Command {
	ws := &cobra.Command{
		Use:   "workspace",
		Short: "Manage Browzer workspaces",
	}

	// list
	var listFilter string
	var listSchema bool
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
			if listSchema {
				if saveFlag != "" {
					return os.WriteFile(saveFlag, []byte(workspaceListSchemaJSON), 0o644)
				}
				fmt.Print(workspaceListSchemaJSON)
				return nil
			}
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
	listCmd.Flags().BoolVar(&listSchema, "schema", false, "Print the JSON schema of the list response and exit")
	listCmd.Flags().Bool("json", false, "Emit JSON instead of plain text")
	listCmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	ws.AddCommand(listCmd)

	// get
	var getSchema bool
	getCmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Fetch a single workspace by id (schema-discovery helper)",
		Args:  cobra.MaximumNArgs(1),
		Long: `Fetch a single workspace by id (schema-discovery helper).

Returns the full workspace DTO so SKILLs can discover the shape
before calling explore/search. Always emits JSON.

Use --schema to print the response JSON schema without making an API call.

Examples:
  browzer workspace get ws-123 --save ws.json
  browzer workspace get --schema --save schema.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if getSchema {
				if saveFlag != "" {
					return os.WriteFile(saveFlag, []byte(workspaceGetSchemaJSON), 0o644)
				}
				fmt.Print(workspaceGetSchemaJSON)
				return nil
			}
			if len(args) == 0 {
				return cliErrors.New("workspace get requires an <id> argument (or use --schema)")
			}
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
	getCmd.Flags().BoolVar(&getSchema, "schema", false, "Print the JSON schema of the get response and exit")
	getCmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout")
	ws.AddCommand(getCmd)

	// delete
	var confirmName string
	var deleteYes bool
	deleteCmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a workspace and all its data",
		Args:  cobra.ExactArgs(1),
		Long: `Delete a workspace and all its data.

Agent-friendly:
  --confirm-name <name>  Required in non-TTY shells. Must match target.Name.
  --yes                  Skip the interactive confirm prompt when combined
                         with --confirm-name. Still requires --confirm-name
                         in non-interactive shells — nothing can delete a
                         workspace without the explicit name match.
  Exit 4 on name mismatch; exit 2 on not found. Recover with:
    browzer workspace list --json   # re-resolve the correct id
    browzer workspace delete <id> --yes --confirm-name <name>

Examples:
  browzer workspace delete ws-123 --confirm-name my-repo
  browzer workspace delete ws-123 --yes --confirm-name my-repo
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
					return cliErrors.Newf(
						"Workspace name confirmation required in non-interactive shells.\n"+
							"Re-run with:\n"+
							"  browzer workspace delete %s --yes --confirm-name %q",
						id, target.Name,
					)
				}
				// In a TTY, --yes alone is NOT enough: we still prompt
				// for the name. --yes only becomes a no-op skip when
				// paired with --confirm-name.
				_ = huh.NewInput().
					Title(fmt.Sprintf("Type the workspace name (%s) to confirm:", target.Name)).
					Value(&confirm).
					Run()
			}
			if confirm != target.Name {
				return cliErrors.Newf("Workspace name confirmation mismatch (expected %q).", target.Name)
			}
			_ = deleteYes // Consumed implicitly: presence of --yes with a matching --confirm-name skips the prompt; without --confirm-name we still prompt above.
			if err := ac.Client.DeleteWorkspace(rootContext(cmd), id); err != nil {
				return err
			}
			ui.Success(fmt.Sprintf("Deleted workspace %s", id))
			return nil
		},
	}
	deleteCmd.Flags().StringVar(&confirmName, "confirm-name", "", "Skip the interactive prompt by passing the workspace name (for non-interactive use)")
	deleteCmd.Flags().BoolVar(&deleteYes, "yes", false, "Skip the interactive prompt (still requires --confirm-name in non-TTY shells)")
	ws.AddCommand(deleteCmd)

	registerWorkspaceUnlink(ws)
	registerWorkspaceRelink(ws)
	registerWorkspaceSync(ws)
	registerWorkspaceDocsList(ws)
	registerWorkspaceFilesList(ws)
	registerWorkspaceShow(ws)

	parent.AddCommand(ws)
	return ws
}
