package commands

import (
	"encoding/json"

	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/spf13/cobra"
)

func registerOrgDocsShow(parent *cobra.Command) {
	var schemaFlag bool

	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a single document by ID",
		Args:  cobra.ExactArgs(1),
		Long: `Show a single document by ID.

Calls GET /api/documents/:id.

Examples:
  browzer org docs show doc-123
  browzer org docs show doc-123 --json
  browzer org docs show doc-123 --save doc.json
  browzer org docs show --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if schemaFlag {
				return schema.PrintOrSave(schema.OrgDocShowSchemaJSON, saveFlag)
			}
			jsonFlag, _ := cmd.Flags().GetBool("json")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			doc, err := ac.Client.GetDocument(rootContext(cmd), args[0])
			if err != nil {
				return err
			}
			human := ""
			if !jsonFlag && saveFlag == "" {
				data, err := json.MarshalIndent(doc, "", "  ")
				if err != nil {
					return err
				}
				human = string(data) + "\n"
			}
			return emitOrFail(doc, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	cmd.Flags().BoolVar(&schemaFlag, "schema", false, "Print the JSON schema of the response and exit")
	parent.AddCommand(cmd)
}
