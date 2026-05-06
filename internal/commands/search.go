package commands

import (
	"fmt"
	"os"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/flags"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

// searchSchemaJSON is the baked-in JSON Schema 2020-12 doc for the
// search response. Mirrors the explore-side `{entries: [...]}` envelope
// (RETRO PR 6 #8 — paridade entre os dois verbos irmãos): consumers can
// pipe both surfaces through the same `.entries[]` jq selector. The
// previous bare-array shape is gone — schema v2 is a hard cutoff.
const searchSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "SearchResponse",
  "type": "object",
  "required": ["entries"],
  "properties": {
    "entries": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["text", "score", "path"],
        "properties": {
          "text":         {"type": "string"},
          "position":     {"type": "integer"},
          "score":        {"type": "number"},
          "path":         {"type": "string"},
          "documentName": {"type": "string"}
        }
      }
    }
  }
}
`

func registerSearch(parent *cobra.Command) {
	var limit int
	var schema bool
	var workspacesCSV string
	var allWorkspaces bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search indexed markdown docs (vector)",
		Args:  cobra.MaximumNArgs(1),
		Long: `For code/symbols/imports, use ` + "`browzer explore`" + ` instead.

Cross-workspace: use --workspaces (CSV list of IDs) or --all-workspaces to fan out
the search across multiple workspaces and merge results. These flags are mutually exclusive.

Examples:
  browzer search "fastify graph store"
  browzer search "device flow" --json
  browzer search "auth flow" --workspaces ws_abc123,ws_def456
  browzer search "auth flow" --all-workspaces
  browzer search --schema --save schema.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate mutual exclusion of --workspaces and --all-workspaces.
			workspaceIDs, err := flags.ParseWorkspacesFlag(workspacesCSV)
			if err != nil {
				return err
			}
			if err := flags.ValidateWorkspaceFlags(workspaceIDs, allWorkspaces); err != nil {
				return err
			}

			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			quietFlag, _ := cmd.Flags().GetBool("quiet")
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

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}

			// Cross-workspace path: use POST /search.
			if len(workspaceIDs) > 0 || allWorkspaces {
				resp, err := ac.Client.SearchCrossWorkspace(rootContext(cmd), api.CrossWorkspaceSearchRequest{
					Query:         query,
					WorkspaceIDs:  workspaceIDs,
					AllWorkspaces: allWorkspaces,
					TopK:          limit,
				})
				if err != nil {
					return err
				}
				converted := make([]output.SearchResult, len(resp.Results))
				for i, r := range resp.Results {
					converted[i] = output.SearchResult{
						Text: r.Text, Position: r.Position,
						Score: r.Score, DocumentName: r.DocumentName,
					}
				}
				if Ultra && len(converted) > 3 {
					converted = converted[:3]
					resp.Results = resp.Results[:3]
				}
				if Ultra {
					for i := range converted {
						converted[i].Score = 0
					}
				}
				// RETRO PR 6 #8: emit `{entries:[...]}` envelope. The
				// previous bare-array shape was sibling-inconsistent with
				// `explore --json` and forced consumers to special-case
				// each verb's jq selector.
				return emitOrFail(
					map[string]any{"entries": resp.Results},
					output.Options{JSON: jsonFlag, Save: saveFlag},
					output.FormatSearchResults(converted),
				)
			}

			// Single-workspace path (legacy).
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

			// RETRO PR 6 §2.1: --quiet suppresses the staleness banner
			// (parity with `workflow describe-step-type --quiet`).
			if !quietFlag {
				if s := git.CheckStaleness(gitRoot, project.LastSyncCommit); s.Stale {
					output.Errf("%s", output.FormatStalenessWarning(s.CommitsBehind))
				}
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
			// RETRO PR 6 #8: same envelope as the cross-workspace branch.
			return emitOrFail(
				map[string]any{"entries": results},
				output.Options{JSON: jsonFlag, Save: saveFlag},
				output.FormatSearchResults(converted),
			)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "Max results (1-200)")
	cmd.Flags().BoolVar(&schema, "schema", false, "Print the JSON schema of the search response and exit")
	cmd.Flags().StringVar(&workspacesCSV, "workspaces", "", "Comma-separated workspace IDs for cross-workspace search (mutually exclusive with --all-workspaces)")
	cmd.Flags().BoolVar(&allWorkspaces, "all-workspaces", false, "Search across all workspaces in the organization (mutually exclusive with --workspaces)")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("save", "", "write JSON to <file> (implies --json)")
	cmd.Flags().Bool("quiet", false, "suppress the staleness warning (parity with workflow read verbs)")
	parent.AddCommand(cmd)
}
