package commands

import (
	"encoding/json"

	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/spf13/cobra"
)

func registerOrg(parent *cobra.Command) *cobra.Command {
	org := &cobra.Command{
		Use:   "org",
		Short: "Organization-level operations",
	}

	var schemaFlag bool

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show the current organization",
		Long: `Show the current organization's details.

Returns the organization DTO for the authenticated caller's org.

Examples:
  browzer org show
  browzer org show --json
  browzer org show --save org.json
  browzer org show --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			if schemaFlag {
				return schema.PrintOrSave(schema.OrgShowSchemaJSON, saveFlag)
			}
			jsonFlag, _ := cmd.Flags().GetBool("json")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			o, err := ac.Client.GetOrganization(rootContext(cmd))
			if err != nil {
				return err
			}
			human := ""
			if !jsonFlag && saveFlag == "" {
				data, err := json.MarshalIndent(o, "", "  ")
				if err != nil {
					return err
				}
				human = string(data) + "\n"
			}
			return emitOrFail(o, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}
	showCmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	showCmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	showCmd.Flags().BoolVar(&schemaFlag, "schema", false, "Print the JSON schema of the response and exit")
	org.AddCommand(showCmd)

	registerOrgMembers(org)
	registerOrgDocs(org)

	parent.AddCommand(org)
	return org
}

