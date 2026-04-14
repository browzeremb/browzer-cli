package commands

import (
	"fmt"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

func registerAsk(parent *cobra.Command) {
	var workspaceFlag string

	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask the RAG engine a question about your codebase",
		Args:  cobra.ExactArgs(1),
		Long: `Ask the Browzer RAG engine a question about your indexed codebase.

The workspace is resolved in this order:
  1. --workspace flag (explicit override)
  2. .browzer/config.json in the git repository root
  3. GET /api/workspaces — first workspace returned by the API
  4. Hard error if all fallbacks fail

Examples:
  browzer ask "How does the answer cache work?"
  browzer ask "What does the reranker do?" --workspace ws_abc123
  browzer ask "Show ingestion pipeline" --json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			question := args[0]

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}

			workspaceID, err := resolveWorkspaceID(cmd, ac, workspaceFlag)
			if err != nil {
				return err
			}

			resp, err := ac.Client.Ask(rootContext(cmd), api.AskRequest{
				Question:    question,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				return err
			}

			human := formatAskResponse(resp)
			return emitOrFail(resp, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
		},
	}

	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "Workspace ID (overrides .browzer/config.json lookup)")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of plain text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}

// resolveWorkspaceID returns the workspace ID to use for an /ask request.
//
// Priority:
//  1. --workspace flag (explicit)
//  2. .browzer/config.json in the git root (local project config)
//  3. GET /api/workspaces — first workspace returned (account-wide fallback)
//  4. Hard error when all fallbacks are exhausted
func resolveWorkspaceID(cmd *cobra.Command, ac *api.AuthenticatedClient, flagValue string) (string, error) {
	// 1. Explicit flag wins.
	if flagValue != "" {
		return flagValue, nil
	}

	// 2. Local project config (.browzer/config.json in git root).
	//    requireGitRoot returns an error when not inside a git repo — treat
	//    that as a soft miss and continue to the API fallback.
	if gitRoot, err := requireGitRoot(); err == nil {
		project, err := config.LoadProjectConfig(gitRoot)
		if err == nil && project != nil && project.WorkspaceID != "" {
			return project.WorkspaceID, nil
		}
	}

	// 3. API fallback — list workspaces and use the first one returned.
	workspaces, err := ac.Client.ListWorkspaces(rootContext(cmd))
	if err != nil {
		return "", fmt.Errorf("could not resolve workspace (API fallback failed): %w", err)
	}
	if len(workspaces) > 0 {
		return workspaces[0].ID, nil
	}

	// 4. All fallbacks exhausted — explicit error so the user knows what to do.
	return "", cliErrors.New(
		"No workspace found. Provide --workspace <id>, run `browzer init` to link a project, or create a workspace first.",
	)
}

// formatAskResponse renders an AskResponse as human-readable text.
func formatAskResponse(resp *api.AskResponse) string {
	var sb strings.Builder
	sb.WriteString(resp.Answer)
	if len(resp.Sources) > 0 {
		sb.WriteString("\n\nSources:\n")
		for i, s := range resp.Sources {
			sb.WriteString(fmt.Sprintf("  %d. %s (score %.3f)\n", i+1, s.DocumentName, s.Score))
		}
	}
	if resp.CacheHit {
		sb.WriteString("\n[answered from cache]\n")
	}
	return sb.String()
}
