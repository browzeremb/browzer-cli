package commands

import (
	"fmt"
	"os"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

// exploreSchemaJSON is the baked-in JSON Schema 2020-12 doc for the
// explore response payload. Returned by `browzer explore --schema` so
// SKILLs can discover the response shape without making an API call.
const exploreSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "ExploreResponse",
  "type": "object",
  "required": ["entries"],
  "properties": {
    "entries": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["path", "type", "name", "score"],
        "properties": {
          "path":      {"type": "string"},
          "type":      {"type": "string", "enum": ["file","folder","symbol"]},
          "name":      {"type": "string"},
          "lineRange": {"type": "string"},
          "snippet":   {"type": "string"},
          "score":     {"type": "number"}
        }
      }
    }
  }
}
`

func registerExplore(parent *cobra.Command) {
	var limit int
	var schema bool

	cmd := &cobra.Command{
		Use:     "explore [query]",
		Aliases: []string{"ask"},
		Short:   "Hybrid graph + vector search across the indexed workspace",
		Args:    cobra.MaximumNArgs(1),
		Long: `Hybrid graph + vector search across the indexed workspace.

Use --schema to print the response JSON schema without making an API call.

Examples:
  browzer explore "auth middleware"
  browzer explore "*.go" --json
  browzer explore --schema --save schema.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			if schema {
				if saveFlag != "" {
					return os.WriteFile(saveFlag, []byte(exploreSchemaJSON), 0o644)
				}
				fmt.Print(exploreSchemaJSON)
				return nil
			}

			if len(args) == 0 || args[0] == "" {
				return cliErrors.New("explore requires a <query> argument (or use --schema)")
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

			// Staleness warning to stderr (never stdout — would pollute --json).
			if s := git.CheckStaleness(gitRoot, project.LastSyncCommit); s.Stale {
				output.Errf("%s", output.FormatStalenessWarning(s.CommitsBehind))
			}

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			entries, err := ac.Client.ExploreWorkspace(rootContext(cmd), project.WorkspaceID, query, limit, 0)
			if err != nil {
				return err
			}

			converted := make([]output.ExploreEntry, len(entries))
			for i, e := range entries {
				converted[i] = output.ExploreEntry{
					Path: e.Path, Type: e.Type, Name: e.Name,
					LineRange: e.LineRange, Snippet: e.Snippet, Score: e.Score,
				}
			}
			// Emit the *converted* entries, not the raw api type, so the
			// JSON shape matches the schema we publish via --schema and
			// future api.ExploreEntry field renames don't silently
			// reshape the SKILL contract.
			return emitOrFail(
				map[string]any{"entries": converted},
				output.Options{JSON: jsonFlag, Save: saveFlag},
				output.FormatExploreResults(converted),
			)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 50, "Max results (1-200)")
	cmd.Flags().BoolVar(&schema, "schema", false, "Print the JSON schema of the explore response and exit")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}

// _exploreReferenceAPIPackage forces api package import (silences the
// compiler if we ever drop the only direct reference). Cheap insurance.
var _ = api.ExploreEntry{}
