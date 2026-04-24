package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

// mentionsSchemaJSON is the baked-in JSON Schema 2020-12 doc for the
// mentions response payload. Returned by `browzer mentions --schema`.
const mentionsSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "MentionsResponse",
  "type": "object",
  "required": ["path", "mentions"],
  "properties": {
    "path":        {"type": "string"},
    "workspaceId": {"type": "string"},
    "mentions": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["doc", "chunkCount"],
        "properties": {
          "doc":            {"type": "string"},
          "chunkCount":     {"type": "integer"},
          "sampleEntities": {"type": "array", "items": {"type": "string"}}
        }
      }
    }
  }
}
`

func registerMentions(parent *cobra.Command) {
	var (
		limit       int
		schema      bool
		workspaceID string
	)

	cmd := &cobra.Command{
		Use:   "mentions <path>",
		Short: "List docs that mention a source file",
		Args:  cobra.MaximumNArgs(1),
		Long: `List indexed documents that mention a given source file via the
MENTIONS graph edge (File ← RELEVANT_TO ← Entity ← MENTIONS ← Chunk ← HAS_CHUNK ← Document).

Used primarily by the update-docs skill to propagate source-file changes
to referencing documentation.

Examples:
  browzer mentions apps/api/src/routes/auth.ts
  browzer mentions apps/api/src/routes/auth.ts --json --save mentions.json
  browzer mentions --schema`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			if schema {
				if saveFlag != "" {
					return os.WriteFile(saveFlag, []byte(mentionsSchemaJSON), 0o644)
				}
				_, err := fmt.Fprint(cmd.OutOrStdout(), mentionsSchemaJSON)
				return err
			}

			if len(args) == 0 || args[0] == "" {
				return cliErrors.New("mentions requires a <path> argument (or use --schema)")
			}
			filePath := args[0]

			if limit < 1 || limit > 100 {
				return cliErrors.New("--limit must be between 1 and 100")
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

			// Resolve the workspace ID (flag → config → API fallback).
			wsID, err := resolveWorkspaceID(cmd, ac, workspaceID)
			if err != nil {
				return err
			}

			// Resolve the path relative to the git root (workspace root).
			// If the user passes an absolute path, strip the gitRoot prefix.
			resolved := filePath
			if filepath.IsAbs(resolved) {
				resolved = git.RealPath(resolved)
				rel, relErr := filepath.Rel(gitRoot, resolved)
				if relErr == nil && !strings.HasPrefix(rel, "..") {
					resolved = rel
				} else {
					return fmt.Errorf("path is outside the workspace: %s", filePath)
				}
			} else if strings.Contains(resolved, "..") {
				abs, absErr := filepath.Abs(resolved)
				if absErr == nil {
					abs = git.RealPath(abs)
					rel, relErr := filepath.Rel(gitRoot, abs)
					if relErr == nil && !strings.HasPrefix(rel, "..") {
						resolved = rel
					} else {
						return fmt.Errorf("path is outside the workspace: %s", filePath)
					}
				} else {
					return fmt.Errorf("failed to resolve path: %w", absErr)
				}
			}
			// else: already a simple relative path — use as-is (workspace-relative)

			resp, err := ac.Client.FetchMentions(rootContext(cmd), wsID, resolved, limit)
			if err != nil {
				return err
			}

			result := output.MentionsResult{
				Path:        resp.Path,
				WorkspaceID: resp.WorkspaceID,
			}
			for _, m := range resp.Mentions {
				result.Mentions = append(result.Mentions, output.MentionItem{
					Doc:            m.Doc,
					ChunkCount:     m.ChunkCount,
					SampleEntities: m.SampleEntities,
				})
			}

			return emitOrFail(
				result,
				output.Options{JSON: jsonFlag, Save: saveFlag},
				output.FormatMentionsResults(result),
			)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 20, "Max docs to return (1-100)")
	cmd.Flags().BoolVar(&schema, "schema", false, "Print the JSON schema of the mentions response and exit")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("save", "", "write JSON to <file> (implies --json)")
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "Override workspace (default: current project)")
	parent.AddCommand(cmd)
}
