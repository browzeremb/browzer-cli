package commands

import (
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

func registerOrgMembers(parent *cobra.Command) {
	members := &cobra.Command{
		Use:   "members",
		Short: "Manage organization members",
	}

	var schemaFlag bool

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List members of the current organization",
		Long: `List all members of the current organization.

Examples:
  browzer org members list
  browzer org members list --json
  browzer org members list --save members.json
  browzer org members list --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if schemaFlag {
				return schema.PrintOrSave(schema.OrgMembersListSchemaJSON, saveFlag)
			}
			jsonFlag, _ := cmd.Flags().GetBool("json")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			resp, err := ac.Client.ListOrgMembers(rootContext(cmd))
			if err != nil {
				return err
			}
			human := "No members.\n"
			if len(resp.Items) > 0 {
				rows := make([][]string, len(resp.Items))
				for i, m := range resp.Items {
					rows[i] = []string{m.UserID, m.Email, m.Role, m.CreatedAt}
				}
				human = ui.Table(
					[]string{"User ID", "Email", "Role", "Joined"},
					rows,
				)
			}
			return emitOrFail(resp, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	listCmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	listCmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	listCmd.Flags().BoolVar(&schemaFlag, "schema", false, "Print the JSON schema of the response and exit")
	members.AddCommand(listCmd)

	parent.AddCommand(members)
}
