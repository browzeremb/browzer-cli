package commands

import (
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

func registerSearch(parent *cobra.Command) {
	var limit int

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Vector search over the indexed markdown documents",
		Args:  cobra.ExactArgs(1),
		Long: `Vector search over the indexed markdown documents.

Examples:
  browzer search "fastify graph store"
  browzer search "device flow" --json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			query := args[0]
			if err := validateLimit(limit); err != nil {
				return err
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

			if s := git.CheckStaleness(gitRoot, project.LastSyncCommit); s.Stale {
				output.Errf("%s", output.FormatStalenessWarning(s.CommitsBehind))
			}

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			results, err := ac.Client.SearchWorkspace(rootContext(cmd), project.WorkspaceID, query, limit, 0)
			if err != nil {
				return err
			}

			converted := make([]output.SearchResult, len(results))
			for i, r := range results {
				converted[i] = output.SearchResult{
					Text: r.Text, Position: r.Position,
					Score: r.Score, DocumentName: r.DocumentName,
				}
			}
			return emitOrFail(
				results,
				output.Options{JSON: jsonFlag, Save: saveFlag},
				output.FormatSearchResults(converted),
			)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "Max results (1-200)")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}
