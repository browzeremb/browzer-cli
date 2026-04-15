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

// depsSchemaJSON is the baked-in JSON Schema 2020-12 doc for the
// deps response payload. Returned by `browzer deps --schema`.
const depsSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "DepsResponse",
  "type": "object",
  "required": ["path"],
  "properties": {
    "path":       {"type": "string"},
    "exports":    {"type": "array", "items": {"type": "string"}},
    "imports":    {"type": "array", "items": {"type": "string"}},
    "importedBy": {"type": "array", "items": {"type": "string"}}
  }
}
`

func registerDeps(parent *cobra.Command) {
	var (
		reverse bool
		limit   int
		schema  bool
	)

	cmd := &cobra.Command{
		Use:   "deps <path>",
		Short: "Show the dependency graph for a single file",
		Args:  cobra.MaximumNArgs(1),
		Long: `Show exports, imports, and reverse-imports for a single file in the
indexed workspace.

Use --reverse to focus on files that import the target.
Use --schema to print the response JSON schema without making an API call.

Examples:
  browzer deps packages/core/src/rag-client/index.ts
  browzer deps ./src/server.ts --reverse --json
  browzer deps --schema
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			if schema {
				if saveFlag != "" {
					return os.WriteFile(saveFlag, []byte(depsSchemaJSON), 0o644)
				}
				fmt.Print(depsSchemaJSON)
				return nil
			}

			if len(args) == 0 || args[0] == "" {
				return cliErrors.New("deps requires a <path> argument (or use --schema)")
			}
			filePath := args[0]
			if limit < 1 || limit > 500 {
				return cliErrors.New("--limit must be between 1 and 500")
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

			// Resolve the path relative to the git root (workspace root).
			// If the user passes an absolute path, strip the gitRoot prefix.
			resolved := filePath
			if filepath.IsAbs(resolved) {
				// Canonicalize to match gitRoot casing on macOS.
				resolved = git.RealPath(resolved)
				rel, relErr := filepath.Rel(gitRoot, resolved)
				if relErr == nil && !strings.HasPrefix(rel, "..") {
					resolved = rel
				} else {
					return fmt.Errorf("path is outside the workspace: %s", filePath)
				}
			} else if strings.Contains(resolved, "..") {
				// Resolve relative to CWD, then strip workspace root.
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

			resp, err := ac.Client.FetchDeps(rootContext(cmd), project.WorkspaceID, resolved, reverse, limit)
			if err != nil {
				return err
			}

			result := output.DepsResult{
				Path:       resp.Path,
				Exports:    resp.Exports,
				Imports:    resp.Imports,
				ImportedBy: resp.ImportedBy,
			}

			// --ultra: only direct edges (truncate to top-10).
			if Ultra {
				if len(result.Imports) > 10 {
					result.Imports = result.Imports[:10]
				}
				if len(result.ImportedBy) > 10 {
					result.ImportedBy = result.ImportedBy[:10]
				}
			}

			return emitOrFail(
				result,
				output.Options{JSON: jsonFlag, Save: saveFlag},
				output.FormatDepsResults(result),
			)
		},
	}

	cmd.Flags().BoolVar(&reverse, "reverse", false, "Focus on reverse imports (files that import the target)")
	cmd.Flags().IntVar(&limit, "limit", 100, "Max results (1-500)")
	cmd.Flags().BoolVar(&schema, "schema", false, "Print the JSON schema of the deps response and exit")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}
