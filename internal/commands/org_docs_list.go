package commands

import (
	"fmt"

	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

func registerOrgDocs(parent *cobra.Command) {
	docs := &cobra.Command{
		Use:   "docs",
		Short: "List and inspect org-scoped documents",
	}

	registerOrgDocsList(docs)
	registerOrgDocsShow(docs)
	registerOrgDocsAdd(docs)

	parent.AddCommand(docs)
}

func registerOrgDocsList(parent *cobra.Command) {
	var schemaFlag bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all documents in the current organization",
		Long: `List all documents across workspaces in the current organization.

Calls GET /api/documents?scope=org.

Examples:
  browzer org docs list
  browzer org docs list --json
  browzer org docs list --save docs.json
  browzer org docs list --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if schemaFlag {
				return schema.PrintOrSave(schema.OrgDocsListSchemaJSON, saveFlag)
			}
			jsonFlag, _ := cmd.Flags().GetBool("json")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			resp, err := ac.Client.ListOrgDocuments(rootContext(cmd))
			if err != nil {
				return err
			}
			human := "No documents.\n"
			if len(resp.Items) > 0 {
				rows := make([][]string, len(resp.Items))
				for i, d := range resp.Items {
					rows[i] = []string{d.ID, d.Name, d.WorkspaceID, d.Status, formatBytes(d.SizeBytes)}
				}
				human = ui.Table(
					[]string{"ID", "Name", "Workspace", "Status", "Size"},
					rows,
				)
			}
			return emitOrFail(resp, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	cmd.Flags().BoolVar(&schemaFlag, "schema", false, "Print the JSON schema of the response and exit")
	parent.AddCommand(cmd)
}

// formatBytes is a small helper shared by org doc commands.
func formatBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
