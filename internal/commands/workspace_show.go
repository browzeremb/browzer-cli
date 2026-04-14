package commands

import (
	"encoding/json"

	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/spf13/cobra"
)

func registerWorkspaceShow(parent *cobra.Command) {
	var schemaFlag bool

	cmd := &cobra.Command{
		Use:   "show <workspace-id>",
		Short: "Show full workspace detail including docs and files",
		Args:  cobra.ExactArgs(1),
		Long: `Show the full detail of a workspace, including indexed documents and files.

Calls GET /api/workspaces/:id?include=docs,files.

Examples:
  browzer workspace show ws-123
  browzer workspace show ws-123 --json
  browzer workspace show ws-123 --save ws.json
  browzer workspace show --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if schemaFlag {
				return schema.PrintOrSave(schema.WorkspaceShowSchemaJSON, saveFlag)
			}
			jsonFlag, _ := cmd.Flags().GetBool("json")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			detail, err := ac.Client.GetWorkspaceDetail(rootContext(cmd), args[0], "docs,files")
			if err != nil {
				return err
			}
			human := ""
			if !jsonFlag && saveFlag == "" {
				data, err := json.MarshalIndent(detail, "", "  ")
				if err != nil {
					return err
				}
				human = string(data) + "\n"
			}
			return emitOrFail(detail, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	cmd.Flags().BoolVar(&schemaFlag, "schema", false, "Print the JSON schema of the response and exit")
	parent.AddCommand(cmd)
}
