package commands

import (
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

func registerWorkspaceDocsList(parent *cobra.Command) {
	var schemaFlag bool

	cmd := &cobra.Command{
		Use:   "docs-list <workspace-id>",
		Short: "List documents indexed in a workspace",
		Args:  cobra.ExactArgs(1),
		Long: `List all documents currently indexed in a workspace.

Calls GET /api/workspaces/:id?include=docs.

Examples:
  browzer workspace docs-list ws-123
  browzer workspace docs-list ws-123 --json
  browzer workspace docs-list ws-123 --save docs.json
  browzer workspace docs-list --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if schemaFlag {
				return schema.PrintOrSave(schema.WorkspaceDocsListSchemaJSON, saveFlag)
			}
			jsonFlag, _ := cmd.Flags().GetBool("json")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			detail, err := ac.Client.GetWorkspaceDetail(rootContext(cmd), args[0], "docs")
			if err != nil {
				return err
			}
			payload := map[string]any{
				"workspaceId": args[0],
				"documents":   detail.Documents,
			}
			human := "No documents.\n"
			if len(detail.Documents) > 0 {
				rows := make([][]string, len(detail.Documents))
				for i, d := range detail.Documents {
					rows[i] = []string{d.DocumentID, d.RelativePath, d.Status}
				}
				human = ui.Table(
					[]string{"Document ID", "Path", "Status"},
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
