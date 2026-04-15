// Package commands — "browzer workspace sync" (and the "browzer sync" top-level alias).
//
// Reconciles the server's indexed set with the local filesystem: re-uploads
// changed docs, deletes docs that were removed locally, keeps unchanged docs.
// Does NOT add new (never-indexed) local files — use "browzer workspace docs"
// for that.
//
// Order is strictly: code index FIRST, then docs. Package nodes must exist
// in the graph before linkEntitiesToWorkspace runs during entity
// extraction — reversing the order means RELEVANT_TO edges are never created
// for documents indexed in the same sync run.
package commands

import (
	"fmt"
	"maps"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/cache"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/browzeremb/browzer-cli/internal/upload"
	"github.com/browzeremb/browzer-cli/internal/walker"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// registerWorkspaceSync wires "browzer workspace sync" (and its top-level
// alias "browzer sync") under the given parent cobra command.
//
// Calling this twice — once on the workspace subgroup, once on root —
// registers both canonical and alias forms, exactly as registerWorkspaceIndex
// does for "browzer index" / "browzer workspace index".
func registerWorkspaceSync(parent *cobra.Command) {
	var (
		dryRun   bool
		skipCode bool
		skipDocs bool
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Re-index code and sync existing workspace docs (no new adds)",
		Long: "Re-index both the repository code structure and documents in a single command.\n\n" +
			"Order of operations (always sequential, never reversed):\n" +
			"  1. Code index  — WalkRepo -> POST /api/workspaces/parse (same as: browzer workspace index)\n" +
			"  2. Document sync — reconcile the server's indexed set with local files:\n" +
			"       * already-indexed, changed locally → re-uploaded\n" +
			"       * already-indexed, deleted locally → deleted from workspace\n" +
			"       * already-indexed, unchanged       → skipped\n" +
			"       * local-only (never indexed)        → ignored (not added)\n\n" +
			"sync only operates on documents already in the workspace. To add new\n" +
			"documents use 'browzer workspace docs' which opens the interactive TUI.\n\n" +
			"The code step runs first so Package nodes exist when linkEntitiesToWorkspace\n" +
			"runs during entity extraction. Reversing the order means RELEVANT_TO edges\n" +
			"are never created for documents indexed in the same sync run.\n\n" +
			"Use --skip-code or --skip-docs to run only one half of the sync:\n" +
			"  browzer workspace sync --skip-docs   # equivalent to: browzer workspace index\n" +
			"  browzer workspace sync --skip-code   # re-sync docs only (no code re-parse)\n\n" +
			"Examples:\n" +
			"  browzer workspace sync\n" +
			"  browzer workspace sync --dry-run\n" +
			"  browzer workspace sync --skip-docs\n" +
			"  browzer workspace sync --json --save sync-result.json\n" +
			output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipCode && skipDocs {
				return cliErrors.New("--skip-code and --skip-docs together leave nothing to do.")
			}

			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			if saveFlag != "" {
				jsonFlag = true
			}
			quiet := jsonFlag || saveFlag != ""

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
			printColdStartHint(quiet)
			ac, err := requireAuth(600)
			if err != nil {
				return err
			}
			client := ac.Client

			if !quiet {
				ui.Arrow(fmt.Sprintf("Workspace: %s", project.WorkspaceID))
			}

			// ----------------------------------------------------------------
			// Shared result accumulators for the final JSON payload.
			// ----------------------------------------------------------------
			codeFiles := 0
			var docPlan DocDeltaPlan

			// ================================================================
			// Step 1: Code index.
			// ================================================================
			if !skipCode {
				sp := startSpinnerQ(quiet, "Walking code tree...")
				tree, walkErr := walker.WalkRepo(gitRoot)
				if walkErr != nil {
					finishSpinnerQ(sp, false, "Walk failed")
					return walkErr
				}
				finishSpinnerQ(sp, true, fmt.Sprintf("Walked code tree (%d files)", len(tree.Files)))
				codeFiles = len(tree.Files)

				if !dryRun {
					sp = startSpinnerQ(quiet, "Re-parsing code on server...")
					if parseErr := client.ParseWorkspace(ctx, api.ParseWorkspaceRequest{
						WorkspaceID: project.WorkspaceID,
						RootPath:    tree.RootPath,
						Folders:     tree.Folders,
						Files:       tree.Files,
					}); parseErr != nil {
						finishSpinnerQ(sp, false, "Parse failed")
						return parseErr
					}
					finishSpinnerQ(sp, true, "Code re-parsed")

					// Stamp LastSyncCommit so "browzer status" can report drift.
					if head := git.HEAD(gitRoot); head != "" {
						project.LastSyncCommit = head
						if saveErr := config.SaveProjectConfig(gitRoot, project); saveErr != nil {
						ui.Warn(fmt.Sprintf("could not save project config: %v", saveErr))
					}
					}
				}
			}

			// ================================================================
			// Step 2: Document sync (non-interactive, selects all local docs).
			// ================================================================
			if !skipDocs {
				// Phase A: fetch server state, local candidates, and billing
				// quota concurrently — same pattern as workspace_docs.go.
				var (
					serverDocs []api.IndexedDocument
					localDocs  []walker.DocFile
					usage      *api.BillingUsageResponse
				)

				sp := startSpinnerQ(quiet, "Loading workspace state...")
				g, gctx := errgroup.WithContext(ctx)
				g.Go(func() error {
					docs, listErr := client.ListWorkspaceDocuments(gctx, project.WorkspaceID)
					if listErr != nil {
						return fmt.Errorf("list workspace documents: %w", listErr)
					}
					serverDocs = docs
					return nil
				})
				g.Go(func() error {
					docs, walkErr := walker.WalkDocs(gitRoot)
					if walkErr != nil {
						return fmt.Errorf("walk local docs: %w", walkErr)
					}
					localDocs = docs
					return nil
				})
				g.Go(func() error {
					u, uErr := client.BillingUsage(gctx)
					if uErr != nil {
						// Quota is nice-to-have — failure should not block sync.
						ui.Warn(fmt.Sprintf("could not fetch billing usage: %v", uErr))
						return nil
					}
					usage = u
					return nil
				})
				if waitErr := g.Wait(); waitErr != nil {
					finishSpinnerQ(sp, false, "Load failed")
					return waitErr
				}
				finishSpinnerQ(sp, true,
					fmt.Sprintf("Loaded %d server doc(s), %d local candidate(s)",
						len(serverDocs), len(localDocs)))

				items := mergeDocItems(localDocs, serverDocs)
				docsCache := cache.Load(gitRoot)

				// mergeDocItems pre-sets Selected=true for every server-indexed item
				// and Selected=false for local-only (never-indexed) files. The only
				// adjustment sync needs: deselect server items whose local file was
				// removed so they land in ToDelete instead of ToKeep.
				for i := range items {
					if items[i].Indexed && !items[i].HasLocal() {
						items[i].Selected = false // deleted locally → remove from server
					}
					// Local-only items (Indexed=false) keep Selected=false and are
					// intentionally skipped. Use "browzer workspace docs" to add them.
				}

				docPlan = computeDocDelta(items, docsCache)

				summary := fmt.Sprintf(
					"Plan: +%d insert, ~%d re-upload, -%d delete, =%d keep",
					len(docPlan.ToInsert), len(docPlan.ToReUpload),
					len(docPlan.ToDelete), len(docPlan.ToKeep),
				)
				if !quiet {
					ui.Arrow(summary)
				}

				if dryRun {
					if !quiet {
						fmt.Println("Dry run — no changes applied.")
					}
					// Fall through to the JSON emitter below.
				} else if len(docPlan.ToInsert) == 0 && len(docPlan.ToReUpload) == 0 && len(docPlan.ToDelete) == 0 {
					if !quiet {
						ui.Success("Workspace documents already in sync.")
					}
				} else {
					// Phase B: preflight, then execute uploads + deletes.
					toUpload := append([]DocPickerItem{}, docPlan.ToInsert...)
					toUpload = append(toUpload, docPlan.ToReUpload...)

					if len(toUpload) > 0 {
						if preErr := runPreflight(ctx, client, toWalkerDocs(toUpload)); preErr != nil {
							return preErr
						}
					}

					// Build the new cache before mutations so we always save
					// even on partial failure, reflecting what was actually done.
					newCache := cache.DocsCache{
						Version: cache.CacheVersion,
						Files:   make(map[string]cache.CachedDoc, len(docsCache.Files)),
					}
					maps.Copy(newCache.Files, docsCache.Files)

					if len(toUpload) > 0 {
						sp = startSpinnerQ(quiet, fmt.Sprintf("Uploading %d doc(s)...", len(toUpload)))
						_, upErr := upload.UploadInBatches(
							ctx, client, &project.WorkspaceID,
							toWalkerDocs(toUpload), &newCache, nil, false,
						)
						if upErr != nil {
							finishSpinnerQ(sp, false, "Upload failed")
							return upErr
						}
						finishSpinnerQ(sp, true, fmt.Sprintf("Uploaded %d doc(s)", len(toUpload)))
					}

					deletesFailed := 0
					var failedDeletes []string
					if len(docPlan.ToDelete) > 0 {
						sp = startSpinnerQ(quiet, fmt.Sprintf("Deleting %d doc(s)...", len(docPlan.ToDelete)))
						for _, d := range docPlan.ToDelete {
							if delErr := client.DeleteDocument(ctx, d.ServerDocumentID, project.WorkspaceID); delErr != nil {
								deletesFailed++
								failedDeletes = append(failedDeletes, fmt.Sprintf("%s: %v", d.RelativePath, delErr))
							} else {
								delete(newCache.Files, d.RelativePath)
							}
						}
						if deletesFailed == 0 {
							finishSpinnerQ(sp, true, fmt.Sprintf("Deleted %d doc(s)", len(docPlan.ToDelete)))
						} else {
							finishSpinnerQ(sp, false,
								fmt.Sprintf("Deleted %d/%d doc(s)", len(docPlan.ToDelete)-deletesFailed, len(docPlan.ToDelete)))
							for _, f := range failedDeletes {
								ui.Warn(f)
							}
						}
					}

					if cacheErr := cache.Save(gitRoot, newCache); cacheErr != nil {
						ui.Warn(fmt.Sprintf("could not save docs cache: %v", cacheErr))
					}

					if !quiet {
						fmt.Println()
						ui.Success("Sync complete")
						if usage != nil {
							fmt.Printf("  Chunks:  %s\n", fmtMeter(usage.Chunks.Used, usage.Chunks.Limit))
							fmt.Printf("  Storage: %s / %s\n",
								humanSize(usage.Storage.Used), humanSize(usage.Storage.Limit))
						}
					}

					if deletesFailed > 0 {
						return cliErrors.Newf("%d document delete(s) failed — see warnings above.", deletesFailed)
					}
				}

				// Emit JSON result when requested.
				if jsonFlag || saveFlag != "" {
					// Refresh usage for the post-sync snapshot (best-effort).
					if !dryRun {
						if u, uErr := client.BillingUsage(ctx); uErr == nil {
							usage = u
						}
					}
					payload := map[string]any{
						"mode":      "sync",
						"dryRun":    dryRun,
						"skipCode":  skipCode,
						"skipDocs":  skipDocs,
						"codeFiles": codeFiles,
						"docs":      buildSubmitPayload(docPlan, usage),
					}
					return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
				}
			} else if jsonFlag || saveFlag != "" {
				// skipDocs=true: emit code-only JSON payload.
				payload := map[string]any{
					"mode":      "sync",
					"dryRun":    dryRun,
					"skipCode":  skipCode,
					"skipDocs":  skipDocs,
					"codeFiles": codeFiles,
				}
				return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without applying changes")
	cmd.Flags().BoolVar(&skipCode, "skip-code", false, "Skip the code re-index step (docs only)")
	cmd.Flags().BoolVar(&skipDocs, "skip-docs", false, "Skip the document sync step (code only, same as browzer workspace index)")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of progress text")
	cmd.Flags().String("save", "", "Write JSON output to <file> instead of stdout (implies --json)")

	parent.AddCommand(cmd)
}

// startSpinnerQ starts a spinner unless quiet mode is active.
// Returns nil in quiet mode so callers can unconditionally pass the result
// to finishSpinnerQ without an extra nil-guard.
func startSpinnerQ(quiet bool, label string) *ui.Spinner {
	if quiet {
		return nil
	}
	return ui.StartSpinner(label)
}

// finishSpinnerQ completes (success or failure) a spinner returned by
// startSpinnerQ. Safe to call with a nil spinner (quiet mode).
func finishSpinnerQ(sp *ui.Spinner, ok bool, msg string) {
	if sp == nil {
		return
	}
	if ok {
		sp.Success(msg)
	} else {
		sp.Failure(msg)
	}
}
