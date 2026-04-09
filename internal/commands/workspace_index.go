// Package commands — `browzer workspace index`.
//
// This file implements the cheap, code-only half of the old `sync`:
// walk the repo, hand the folders/files/symbols tree to the server's
// regex parser, done. No document handling, no embedding, no quota
// preflight (the code graph doesn't consume chunk budget). Document
// re-indexing lives in `browzer workspace docs` — the interactive TUI.
//
// Retained behaviors from the retired `sync.go`:
//   - cold-start 600s auth timeout (embedding model may still be
//     warming even though we don't embed code, matching precedent).
//   - --dry-run / --json / --save plumbing via emitOrFail.
//   - Stamps ProjectConfig.LastSyncCommit on success when git HEAD is
//     resolvable, so `browzer status` can still report drift.
package commands

import (
	"fmt"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/browzeremb/browzer-cli/internal/walker"
	"github.com/spf13/cobra"
)

// registerWorkspaceIndex wires `browzer workspace index` under the given
// parent cobra command. It is called from both the top-level root (as
// the `browzer index` alias) and the `workspace` subgroup.
func registerWorkspaceIndex(parent *cobra.Command) {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Re-parse the repository's code structure into the workspace graph",
		Long: `Re-parse the repository's code structure into the workspace graph.

This is the cheap command: it walks the repo tree and hands the
folders/files/symbols to the server's regex parser. It does NOT
touch documents, does NOT consume chunk quota, and does NOT embed
anything.

To re-index markdown, PDFs and other documents, use the interactive
picker:

  browzer workspace docs

Examples:
  browzer workspace index
  browzer workspace index --dry-run
  browzer workspace index --json --save index.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

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

			// --save implies --json: the saved file must be machine
			// parseable regardless of whether the caller remembered
			// to pass --json.
			if saveFlag != "" {
				jsonFlag = true
			}

			ctx := rootContext(cmd)
			quiet := jsonFlag || saveFlag != ""

			// Mirror sync.go's spinner gating: no progress UI in quiet
			// mode so JSON on stdout stays parseable.
			startStep := func(label string) *ui.Spinner {
				if quiet {
					return nil
				}
				return ui.StartSpinner(label)
			}
			finishStep := func(sp *ui.Spinner, okMsg string) {
				if sp != nil {
					sp.Success(okMsg)
				}
			}
			failStep := func(sp *ui.Spinner, msg string) {
				if sp != nil {
					sp.Failure(msg)
				}
			}

			if !quiet {
				ui.Arrow(fmt.Sprintf("Workspace: %s", project.WorkspaceID))
			}

			printColdStartHint(quiet)
			ac, err := requireAuth(600) // cold-start tolerance
			if err != nil {
				return err
			}
			client := ac.Client

			sp := startStep("Walking code tree...")
			tree, err := walker.WalkRepo(gitRoot)
			if err != nil {
				failStep(sp, "Walk failed")
				return err
			}
			finishStep(sp, fmt.Sprintf("Walked code tree (%d files)", len(tree.Files)))

			if dryRun {
				payload := map[string]any{
					"mode":        "dry-run",
					"workspaceId": project.WorkspaceID,
					"codeFiles":   len(tree.Files),
				}
				human := fmt.Sprintf("\nDry run: would re-parse code (%d files)\n", len(tree.Files))
				return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
			}

			sp = startStep("Re-parsing code on server...")
			if err := client.ParseWorkspace(ctx, api.ParseWorkspaceRequest{
				WorkspaceID: project.WorkspaceID,
				RootPath:    tree.RootPath,
				Folders:     tree.Folders,
				Files:       tree.Files,
			}); err != nil {
				failStep(sp, "Parse failed")
				return err
			}
			finishStep(sp, "Code re-parsed")

			// Stamp LastSyncCommit so `browzer status` can show drift.
			// Best-effort — a failure here doesn't undo the server-side
			// parse, so we swallow the save error.
			if head := git.HEAD(gitRoot); head != "" {
				// Mutate the loaded project struct in place rather
				// than constructing a fresh literal — preserves any
				// current or future fields we don't explicitly care
				// about here.
				project.LastSyncCommit = head
				_ = config.SaveProjectConfig(gitRoot, project)
			}

			if !quiet {
				fmt.Println()
				ui.Success("Code re-indexed")
			}

			if quiet {
				payload := map[string]any{
					"mode":        "index",
					"workspaceId": project.WorkspaceID,
					"codeFiles":   len(tree.Files),
				}
				return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would happen without calling the server")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of progress text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}
