package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/cache"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/browzeremb/browzer-cli/internal/upload"
	"github.com/browzeremb/browzer-cli/internal/walker"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

func registerInit(parent *cobra.Command) {
	var force bool
	var nameFlag string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Register the current git repo as a Browzer workspace and index its contents",
		Long: `Register the current git repository as a Browzer workspace and index its
contents (code parse + docs upload) in one shot.

If anything fails after the workspace is created, the CLI rolls back by
calling DELETE /api/workspaces/:id and POST /api/documents/batch/:id/cancel
on every in-flight batch — keeps the server clean for retries.

Examples:
  browzer init --name my-repo
  browzer init --dry-run --json
  browzer init --dry-run --save plan.json
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			gitRoot, err := requireGitRoot()
			if err != nil {
				return cliErrors.New("Not inside a git repository. Run `git init` first or change directory.")
			}

			ctx := rootContext(cmd)

			if dryRun {
				return runInitDryRun(gitRoot, nameFlag, jsonFlag, saveFlag)
			}

			existing, err := config.LoadProjectConfig(gitRoot)
			if err != nil {
				return err
			}
			if existing != nil && !force {
				return cliErrors.Newf("Already initialized (workspaceId=%s). Use --force to overwrite.", existing.WorkspaceID)
			}

			defaultName := filepath.Base(gitRoot)
			name := resolveWorkspaceName(defaultName, nameFlag)

			// init never runs in --json/--save mode without --dry-run
			// (which short-circuits earlier), so we always honor the
			// hint here. quiet=false → print to stderr.
			printColdStartHint(jsonFlag || saveFlag != "")
			ac, err := requireAuth(600) // cold-start tolerance
			if err != nil {
				return err
			}
			client := ac.Client

			reusingExisting := existing != nil && force
			var workspaceID, workspaceName string
			if reusingExisting {
				ui.Arrow(fmt.Sprintf("Reusing workspace %s (--force)", existing.WorkspaceID))
				workspaceID = existing.WorkspaceID
				workspaceName = name
			} else {
				sp := ui.StartSpinner("Creating workspace...")
				ws, err := client.CreateWorkspace(ctx, api.CreateWorkspaceRequest{Name: name, RootPath: gitRoot})
				if err != nil {
					sp.Failure("Create workspace failed")
					return cliErrors.Newf("Failed to create workspace (%s).", err.Error())
				}
				workspaceID = ws.ID
				workspaceName = ws.Name
				sp.Success(fmt.Sprintf("Workspace created: %s", workspaceID))
			}

			var inflightBatches []string

			rollback := func(reason error) error {
				if reusingExisting {
					ui.Failure(fmt.Sprintf("Init failed mid-reparse for workspace %s. Local config and existing server data are unchanged. Retry with `browzer init --force` or `browzer sync`.", workspaceID))
					return reason
				}
				ui.Failure(fmt.Sprintf("Init failed after creating workspace %s — rolling back...", workspaceID))
				rollbackAC, rerr := requireAuth(0)
				if rerr != nil {
					ui.Warn(fmt.Sprintf("Rollback aborted: %v", rerr))
					return reason
				}
				for _, bid := range inflightBatches {
					if err := rollbackAC.Client.CancelBatch(ctx, bid); err != nil {
						ui.Warn(fmt.Sprintf("Could not cancel batch %s (%v) — proceeding with delete", bid, err))
					} else {
						ui.SuccessTo(os.Stderr, fmt.Sprintf("Cancelled batch %s", bid))
					}
				}
				if err := rollbackAC.Client.DeleteWorkspace(ctx, workspaceID); err != nil {
					ui.Warn(fmt.Sprintf("Rollback failed (%v). Run `browzer workspace delete %s` manually.", err, workspaceID))
				} else {
					ui.SuccessTo(os.Stderr, "Rolled back")
				}
				return reason
			}

			// 2. Walk + parse code tree.
			sp := ui.StartSpinner("Walking code tree...")
			tree, err := walker.WalkRepo(gitRoot)
			if err != nil {
				sp.Failure("Walk failed")
				return rollback(err)
			}
			sp.Success(fmt.Sprintf("Walked code tree (%d files)", len(tree.Files)))

			sp = ui.StartSpinner("Parsing code on server...")
			if err := client.ParseWorkspace(ctx, api.ParseWorkspaceRequest{
				WorkspaceID: workspaceID,
				RootPath:    tree.RootPath,
				Folders:     tree.Folders,
				Files:       tree.Files,
			}); err != nil {
				sp.Failure("Parse failed")
				return rollback(err)
			}
			sp.Success("Code parsed")

			// 3. Walk + upload docs (full upload, fresh workspace).
			sp = ui.StartSpinner("Walking docs...")
			docs, err := walker.WalkDocs(gitRoot)
			if err != nil {
				sp.Failure("Docs walk failed")
				return rollback(err)
			}
			sp.Success(fmt.Sprintf("Walked docs (%d files)", len(docs)))

			docsCache := cache.DocsCache{Version: cache.CacheVersion, Files: map[string]cache.CachedDoc{}}
			if len(docs) > 0 {
				sp = ui.StartSpinner(fmt.Sprintf("Uploading %d docs...", len(docs)))
				res, err := upload.UploadInBatches(ctx, client, workspaceID, docs, &docsCache, func(bid string) {
					inflightBatches = append(inflightBatches, bid)
				}, false)
				if err != nil {
					sp.Failure("Upload failed")
					return rollback(err)
				}
				sp.Success(fmt.Sprintf("Uploaded %d docs", len(docs)))
				if res.FailedCount > 0 {
					ui.Warn(fmt.Sprintf("%d doc(s) failed — see warnings above", res.FailedCount))
				}
			}

			// 4. Persist cache + config + .gitignore.
			if err := cache.Save(gitRoot, docsCache); err != nil {
				return rollback(err)
			}
			if err := config.AddCacheDirToGitignore(gitRoot); err != nil {
				ui.Warn(fmt.Sprintf("Could not update .gitignore (%v). Add \".browzer/.cache/\" manually.", err))
			}

			if err := config.SaveProjectConfig(gitRoot, &config.ProjectConfig{
				Version:       config.ProjectConfigVersion,
				WorkspaceID:   workspaceID,
				WorkspaceName: workspaceName,
				Server:        ac.Credentials.Server,
			}); err != nil {
				return rollback(err)
			}

			verb := "created and indexed"
			if reusingExisting {
				verb = "re-indexed"
			}
			fmt.Println()
			ui.Success(fmt.Sprintf("Workspace %q %s (%s)", workspaceName, verb, workspaceID))
			ui.Success("Wrote .browzer/config.json")
			fmt.Println("\nNext:\n  browzer status\n  browzer search \"...\"\n  browzer explore \"...\"")
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing .browzer/config.json")
	cmd.Flags().StringVar(&nameFlag, "name", "", "Workspace name (default: git repo basename)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Walk the repo and report what would be indexed without calling the server")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}

func runInitDryRun(gitRoot, nameFlag string, jsonFlag bool, saveFlag string) error {
	defaultName := filepath.Base(gitRoot)
	name := nameFlag
	if name == "" {
		name = defaultName
	}
	tree, err := walker.WalkRepo(gitRoot)
	if err != nil {
		return err
	}
	docs, err := walker.WalkDocs(gitRoot)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"mode":          "dry-run",
		"gitRoot":       gitRoot,
		"workspaceName": name,
		"codeFiles":     len(tree.Files),
		"docs":          len(docs),
	}
	human := fmt.Sprintf("Dry run:\n  name:  %s\n  root:  %s\n  code:  %d files\n  docs:  %d files\n", name, gitRoot, len(tree.Files), len(docs))
	return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
}

// resolveWorkspaceName picks the workspace name from --name → TTY prompt
// → repo basename. Non-interactive shells silently fall back so CI can
// `browzer init` without piping input.
func resolveWorkspaceName(defaultName, flagName string) string {
	if flagName != "" {
		return flagName
	}
	if !isTTY() {
		return defaultName
	}
	value := defaultName
	err := huh.NewInput().
		Title("Workspace name:").
		Value(&value).
		Run()
	if err != nil || value == "" {
		return defaultName
	}
	return value
}

// _ is a compile-time guard ensuring context.Context is referenced even
// if the file is reorganised — keeps imports stable for refactors.
var _ = context.Background
