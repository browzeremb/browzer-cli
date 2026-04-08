package commands

import (
	"fmt"
	"os"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/cache"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/upload"
	"github.com/browzeremb/browzer-cli/internal/walker"
	"github.com/spf13/cobra"
)

func registerSync(parent *cobra.Command) {
	var noDocs bool
	var dryRun bool
	var noWait bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Re-index the current repository against its Browzer workspace",
		Long: `Re-index the current repository against its Browzer workspace.

Strategy:
  1. Full re-parse of the code graph (cheap server-side regex parser).
  2. Delta upload of markdown docs via per-machine SHA-256 cache.
  3. Singular DELETE for each removed doc; failed deletes exit non-zero.

Examples:
  browzer sync
  browzer sync --dry-run --json
  browzer sync --no-wait --json    # async; pair with browzer job get
  browzer sync --json --save sync.json
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

			ctx := rootContext(cmd)
			quiet := jsonFlag || saveFlag != "" || noWait
			writeStatus := func(s string) {
				if !quiet {
					fmt.Print(s)
				}
			}

			writeStatus(fmt.Sprintf("→ Workspace: %s\n", project.WorkspaceID))

			ac, err := requireAuth(600) // cold-start tolerance
			if err != nil {
				return err
			}
			client := ac.Client

			writeStatus("  Walking code tree... ")
			tree, err := walker.WalkRepo(gitRoot)
			if err != nil {
				return err
			}
			writeStatus(fmt.Sprintf("✓ (%d files)\n", len(tree.Files)))

			var (
				docsAdded, docsModified, docsDeleted int
				diffPlan                             *cache.Diff
				docsCache                            cache.DocsCache
			)
			if !noDocs {
				writeStatus("  Walking docs... ")
				docs, err := walker.WalkDocs(gitRoot)
				if err != nil {
					return err
				}
				docsCache = cache.Load(gitRoot)
				diff := cache.DiffDocs(docs, docsCache)
				docsAdded = len(diff.Added)
				docsModified = len(diff.Modified)
				docsDeleted = len(diff.Deleted)
				diffPlan = &diff
				writeStatus(fmt.Sprintf("✓ (+%d ~%d -%d)\n", docsAdded, docsModified, docsDeleted))
			}

			if dryRun {
				payload := map[string]any{
					"mode":        "dry-run",
					"workspaceId": project.WorkspaceID,
					"codeFiles":   len(tree.Files),
					"docs": map[string]int{
						"added": docsAdded, "modified": docsModified, "deleted": docsDeleted,
					},
				}
				human := fmt.Sprintf("\nDry run: would re-parse code (%d files), docs +%d ~%d -%d\n",
					len(tree.Files), docsAdded, docsModified, docsDeleted)
				return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
			}

			writeStatus("  Re-parsing code on server... ")
			if err := client.ParseWorkspace(ctx, api.ParseWorkspaceRequest{
				WorkspaceID: project.WorkspaceID,
				RootPath:    tree.RootPath,
				Folders:     tree.Folders,
				Files:       tree.Files,
			}); err != nil {
				return err
			}
			writeStatus("✓\n")

			deletesFailed := 0
			var inflightBatchIDs []string

			if diffPlan != nil {
				toUpload := append(diffPlan.Added, diffPlan.Modified...)
				newCache := cache.DocsCache{
					Version: cache.CacheVersion,
					Files:   make(map[string]cache.CachedDoc, len(docsCache.Files)),
				}
				for k, v := range docsCache.Files {
					newCache.Files[k] = v
				}

				if len(toUpload) > 0 {
					writeStatus(fmt.Sprintf("  Uploading %d docs... ", len(toUpload)))
					res, err := upload.UploadInBatches(ctx, client, project.WorkspaceID, toUpload, &newCache, func(bid string) {
						inflightBatchIDs = append(inflightBatchIDs, bid)
					}, noWait)
					if err != nil {
						return err
					}
					writeStatus("✓\n")
					_ = res
				}

				for _, d := range diffPlan.Deleted {
					if err := client.DeleteDocument(ctx, d.DocumentID, project.WorkspaceID); err != nil {
						deletesFailed++
						fmt.Fprintf(os.Stderr, "  ⚠ delete %s: %v\n", d.RelativePath, err)
					} else {
						delete(newCache.Files, d.RelativePath)
					}
				}
				if err := cache.Save(gitRoot, newCache); err != nil {
					return err
				}
			}

			// Stamp lastSyncCommit if we can resolve HEAD.
			if head := git.HEAD(gitRoot); head != "" {
				_ = config.SaveProjectConfig(gitRoot, &config.ProjectConfig{
					Version:        config.ProjectConfigVersion,
					WorkspaceID:    project.WorkspaceID,
					WorkspaceName:  project.WorkspaceName,
					Server:         project.Server,
					CreatedAt:      project.CreatedAt,
					LastSyncCommit: head,
				})
			}

			if deletesFailed > 0 {
				return cliErrors.Newf("Sync completed with %d delete failure(s) — see warnings above.", deletesFailed)
			}

			writeStatus("\n✓ Sync complete\n")

			if quiet {
				payload := map[string]any{
					"mode":        ifS(noWait, "sync-async", "sync"),
					"workspaceId": project.WorkspaceID,
					"codeFiles":   len(tree.Files),
					"docs": map[string]int{
						"added": docsAdded, "modified": docsModified, "deleted": docsDeleted,
					},
					"deletesFailed": deletesFailed,
					"batchIds":      inflightBatchIDs,
				}
				human := ""
				if noWait {
					human = fmt.Sprintf("\nEnqueued %d batch(es). Poll with: browzer job get <batchId> --json\n", len(inflightBatchIDs))
				}
				return emitOrFail(payload, output.Options{JSON: jsonFlag || noWait, Save: saveFlag}, human)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&noDocs, "no-docs", false, "Skip markdown re-indexing (only re-parse code)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would happen without calling the server")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Enqueue doc batches and return batchIds without polling (pair with `browzer job get`)")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of progress text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")
	parent.AddCommand(cmd)
}

// ifS is a tiny ternary helper for string outcomes — keeps the
// payload-building block above readable.
func ifS(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
