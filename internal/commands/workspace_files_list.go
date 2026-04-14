package commands

import (
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

func registerWorkspaceFilesList(parent *cobra.Command) {
	var schemaFlag bool

	cmd := &cobra.Command{
		Use:   "files-list <workspace-id>",
		Short: "List files indexed in a workspace",
		Args:  cobra.ExactArgs(1),
		Long: `List all files currently indexed in a workspace.

Calls GET /api/workspaces/:id?include=files.

Examples:
  browzer workspace files-list ws-123
  browzer workspace files-list ws-123 --json
  browzer workspace files-list ws-123 --save files.json
  browzer workspace files-list --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if schemaFlag {
				return schema.PrintOrSave(schema.WorkspaceFilesListSchemaJSON, saveFlag)
			}
			jsonFlag, _ := cmd.Flags().GetBool("json")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			detail, err := ac.Client.GetWorkspaceDetail(rootContext(cmd), args[0], "files")
			if err != nil {
				return err
			}
			payload := map[string]any{
				"workspaceId": args[0],
				"files":       detail.Files,
			}
			human := "No files.\n"
			if len(detail.Files) > 0 {
				rows := make([][]string, len(detail.Files))
				for i, f := range detail.Files {
					rows[i] = []string{f.Path, f.Language}
				}
				human = ui.Table(
					[]string{"Path", "Language"},
					rows,
				)
			}
			return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	cmd.Flags().BoolVar(&schemaFlag, "schema", false, "Print the JSON schema of the response and exit")
	parent.AddCommand(cmd)
}
