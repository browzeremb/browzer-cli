package commands

import (
	"fmt"
	"os"

	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

// searchSchemaJSON is the baked-in JSON Schema 2020-12 doc for the
// search response (an array of SearchResult entries).
const searchSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "SearchResponse",
  "type": "array",
  "items": {
    "type": "object",
    "required": ["text", "score"],
    "properties": {
      "text":         {"type": "string"},
      "position":     {"type": "integer"},
      "score":        {"type": "number"},
      "documentName": {"type": "string"}
    }
  }
}
`

func registerSearch(parent *cobra.Command) {
	var limit int
	var schema bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search indexed markdown docs (vector)",
		Args:  cobra.MaximumNArgs(1),
		Long: `For code/symbols/imports, use ` + "`browzer explore`" + ` instead.

Examples:
  browzer search "fastify graph store"
  browzer search "device flow" --json
  browzer search --schema --save schema.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			if schema {
				if saveFlag != "" {
					return os.WriteFile(saveFlag, []byte(searchSchemaJSON), 0o644)
				}
				fmt.Print(searchSchemaJSON)
				return nil
			}
			if len(args) == 0 || args[0] == "" {
				return cliErrors.New("search requires a <query> argument (or use --schema)")
			}
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
			// --ultra: top-3 results, drop score metadata.
			if Ultra && len(converted) > 3 {
				converted = converted[:3]
				results = results[:3]
			}
			if Ultra {
				for i := range converted {
					converted[i].Score = 0
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
	cmd.Flags().BoolVar(&schema, "schema", false, "Print the JSON schema of the search response and exit")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("save", "", "write JSON to <file> (implies --json)")
	parent.AddCommand(cmd)
}
