// Package commands — "browzer workspace sync" (and the "browzer sync" top-level alias).
//
// Reconciles the server's indexed set with the local filesystem: re-uploads
// changed docs, deletes docs that were removed locally, adds new local docs
// that pass the stacked ignore filter (.gitignore ∩ .browzerignore), and keeps
// unchanged docs.
//
// Order is strictly: code index FIRST, then docs. Package nodes must exist
// in the graph before linkEntitiesToWorkspace runs during entity
// extraction — reversing the order means RELEVANT_TO edges are never created
// for documents indexed in the same sync run.
package commands

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

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

// syncFlowOptions is the parsed, normalized input to runSyncFlow. Each
// public-facing cobra.Command (workspace sync, workspace index,
// workspace docs default path) builds one of these and calls
// runSyncFlow. This keeps observable output identical across the three
// surfaces that PRD FR-5 and FR-6 demand be equivalent.
type syncFlowOptions struct {
	DryRun         bool
	SkipCode       bool
	SkipDocs       bool
	Force          bool
	NoWait         bool
	Yes            bool
	ConfirmAdds    int
	ConfirmDeletes int
	// Output
	JSON bool
	Save string
	// JSONMode is the value emitted as "mode" in the machine-readable
	// payload. "sync" for browzer sync; "index" for browzer workspace
	// index (backward-compat). workspace docs default path uses "sync".
	JSONMode string
}

// runSyncFlowHook is a seam for tests. Production always uses runSyncFlow.
// Tests can override this to intercept calls and assert opts without
// running the full network-dependent flow.
var runSyncFlowHook = runSyncFlow

// runSyncFlow executes the unified reconciliation flow. Used by
// workspace sync (direct), workspace index (SkipDocs=true alias), and
// workspace docs no-flags path (SkipCode=true alias). All three
// produce observably identical output modulo the SkipCode/SkipDocs
// booleans and JSONMode.
func runSyncFlow(ctx context.Context, opts syncFlowOptions) error {
	jsonFlag := opts.JSON
	saveFlag := opts.Save
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

	printColdStartHint(quiet)
	ac, err := requireAuth(600)
	if err != nil {
		return err
	}
	client := ac.Client

	if !quiet {
		ui.Arrow(fmt.Sprintf("Workspace: %s", project.WorkspaceID))
	}

	// PR 3 — Fase 0: jobs-in-flight preflight (code path only;
	// document sync uploads new ingestion jobs and is therefore
	// not affected by pending parses). Skipped under --force or
	// --skip-code. See preflightJobsInFlight in workspace_index.go.
	if !opts.SkipCode && !opts.Force {
		if abortErr := preflightJobsInFlight(ctx, client, project.WorkspaceID, quiet); abortErr != nil {
			return abortErr
		}
	}

	// ----------------------------------------------------------------
	// Shared result accumulators for the final JSON payload.
	// ----------------------------------------------------------------
	codeFiles := 0
	var docPlan DocDeltaPlan

	// ================================================================
	// Step 1: Code index.
	// ================================================================
	if !opts.SkipCode {
		sp := startSpinnerQ(quiet, "Walking code tree...")
		tree, walkErr := walker.WalkRepo(gitRoot)
		if walkErr != nil {
			finishSpinnerQ(sp, false, "Walk failed")
			return walkErr
		}
		finishSpinnerQ(sp, true, fmt.Sprintf("Walked code tree (%d files)", len(tree.Files)))
		codeFiles = len(tree.Files)

		if !opts.DryRun {
			sp = startSpinnerQ(quiet, "Re-parsing code on server...")
			parseResp, parseErr := client.ParseWorkspace(ctx, api.ParseWorkspaceRequest{
				WorkspaceID: project.WorkspaceID,
				RootPath:    tree.RootPath,
				Folders:     tree.Folders,
				Files:       tree.Files,
			}, api.ParseWorkspaceOptions{ForceParse: opts.Force})
			if parseErr != nil {
				finishSpinnerQ(sp, false, "Parse failed")
				return parseErr
			}
			if parseResp != nil && parseResp.Status == "unchanged" {
				finishSpinnerQ(sp, true, "No changes detected — skipped re-parse")
			} else {
				finishSpinnerQ(sp, true, "Code re-parsed")
			}

			// Stamp LastSyncCommit so "browzer status" can report drift.
			if head := git.HEAD(gitRoot); head != "" {
				project.LastSyncCommit = head
				if saveErr := config.SaveProjectConfig(gitRoot, project); saveErr != nil {
					ui.Warn(fmt.Sprintf("could not save project config: %v", saveErr))
				}
			}

			// Pull the per-file manifest so the daemon's ManifestCache
			// can back `filterLevel: "aggressive"` in `browzer read` +
			// the rewrite-read hook. Best-effort — stale cache just
			// downgrades aggressive → minimal.
			if err := pullAndSaveManifest(ctx, client, project.WorkspaceID); err != nil {
				if !quiet {
					ui.Warn(fmt.Sprintf("could not cache workspace manifest: %v", err))
				}
			}
		}
	}

	// ================================================================
	// Step 2: Document sync (non-interactive, selects all local docs).
	// ================================================================
	if !opts.SkipDocs {
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
				// F-13 (FR-4): suppress the noisy "Forbidden" warning that
				// fires on every successful sync against a default member-
				// role API key (which lacks billing read scope). Other
				// errors still surface so genuine breakage is visible.
				if !api.IsBillingForbidden(uErr) {
					ui.Warn(fmt.Sprintf("could not fetch billing usage: %v", uErr))
				}
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

		// applySyncSelection adjusts each item's Selected flag:
		//   - server-indexed + no local file   → deselect (DELETE class)
		//   - local-only + local file present  → select (ADD class)
		//     WalkDocs has already filtered by .gitignore ∩ .browzerignore,
		//     so any survivor here is eligible for indexing.
		items = applySyncSelection(items)

		docPlan = computeDocDelta(items, docsCache)

		// Confirmation thresholds: abort when the plan exceeds the configured
		// ADD or DELETE count unless --yes is set or this is a dry-run.
		if !opts.DryRun && !opts.Yes {
			if len(docPlan.ToInsert) > opts.ConfirmAdds {
				return cliErrors.WithCode(fmt.Sprintf(
					"plan would ADD %d doc(s) (threshold: %d). Re-run with --yes to confirm, or raise --confirm-adds.",
					len(docPlan.ToInsert), opts.ConfirmAdds,
				), cliErrors.ExitTotalFailure)
			}
			if len(docPlan.ToDelete) > opts.ConfirmDeletes {
				return cliErrors.WithCode(fmt.Sprintf(
					"plan would DELETE %d doc(s) (threshold: %d). Re-run with --yes to confirm, or raise --confirm-deletes.",
					len(docPlan.ToDelete), opts.ConfirmDeletes,
				), cliErrors.ExitTotalFailure)
			}
		}

		summary := fmt.Sprintf(
			"Plan: +%d insert, ~%d re-upload, -%d delete, =%d keep",
			len(docPlan.ToInsert), len(docPlan.ToReUpload),
			len(docPlan.ToDelete), len(docPlan.ToKeep),
		)
		if !quiet {
			ui.Arrow(summary)
		}

		if opts.DryRun {
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
				// Proactive warning when the org's daily ingestion
				// counter is near the per-plan cap. Best-effort: silent on
				// Redis-miss or pre-2026-04-22 servers that omit
				// the `ingestion_daily` field. Warning only — never
				// blocks the sync.
				if u, uErr := client.BillingUsage(ctx); uErr == nil &&
					u.IngestionDaily != nil && u.IngestionDaily.Limit > 0 {
					used := u.IngestionDaily.Used
					limit := u.IngestionDaily.Limit
					batch := int64(len(toUpload))
					switch {
					case used+batch > limit:
						ui.Warn(fmt.Sprintf(
							"daily ingestion cap would be exceeded: %d used + %d to upload > %d limit. "+
								"Some uploads will be rejected. Reset ~%s.",
							used, batch, limit, fmtResetAt(u.IngestionDaily.ResetAt),
						))
					case used >= limit*8/10:
						ui.Warn(fmt.Sprintf(
							"daily ingestion counter near cap: %d / %d (resets %s)",
							used, limit, fmtResetAt(u.IngestionDaily.ResetAt),
						))
					}
				}
			}

			// Build the new cache before mutations so we always save
			// even on partial failure, reflecting what was actually done.
			newCache := cache.DocsCache{
				Version: cache.CacheVersion,
				Files:   make(map[string]cache.CachedDoc, len(docsCache.Files)),
			}
			maps.Copy(newCache.Files, docsCache.Files)

			var uploadResult upload.Result
			if len(toUpload) > 0 {
				sp = startSpinnerQ(quiet, fmt.Sprintf("Uploading %d doc(s)...", len(toUpload)))
				r, upErr := upload.UploadInBatches(
					ctx, client, &project.WorkspaceID,
					toWalkerDocs(toUpload), &newCache, nil, opts.NoWait,
				)
				if upErr != nil {
					finishSpinnerQ(sp, false, "Upload failed")
					return upErr
				}
				uploadResult = r
				switch {
				case opts.NoWait:
					finishSpinnerQ(sp, true,
						fmt.Sprintf("Enqueued %d doc(s) (batch IDs: %s)",
							len(toUpload), strings.Join(r.BatchIDs, ", ")))
				case r.FailedCount > 0:
					finishSpinnerQ(sp, false,
						fmt.Sprintf("⚠ %d of %d doc(s) failed", r.FailedCount, len(toUpload)))
					for _, name := range r.FailedNames {
						output.Errf("  ⚠ %s\n", name)
					}
					if r.SkippedCount > 0 {
						output.Errf("  (%d doc(s) skipped — already indexed with same content)\n", r.SkippedCount)
					}
				case r.SkippedCount > 0 && r.UploadedCount == 0:
					finishSpinnerQ(sp, true,
						fmt.Sprintf("All %d doc(s) already indexed (no-op)", r.SkippedCount))
				case r.SkippedCount > 0:
					finishSpinnerQ(sp, true,
						fmt.Sprintf("Uploaded %d doc(s), skipped %d already-indexed",
							r.UploadedCount, r.SkippedCount))
				default:
					finishSpinnerQ(sp, true,
						fmt.Sprintf("Uploaded %d doc(s)", r.UploadedCount))
				}
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

			// Refresh usage AFTER uploads so the printed Chunks/Storage
			// reflects what just landed. The pre-sync `usage` captured
			// at line ~199 was before any docs were ingested, so a sync
			// that just inserted N chunks would still report
			// "Chunks: 0 / 200000" without this refresh. Best-effort —
			// failure leaves the pre-sync snapshot in place. Skipped on
			// --no-wait because chunks land asynchronously after the
			// CLI returns; the counter would still be lagging anyway.
			if !opts.DryRun && !opts.NoWait {
				if u, uErr := client.BillingUsage(ctx); uErr == nil {
					usage = u
				}
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

			// Same exit-code taxonomy as workspace_docs.go (ec37933):
			//   7 (ExitPartialFailure) — some docs failed, at least one succeeded
			//   8 (ExitTotalFailure)   — all docs failed (no completions)
			// Skipped under --no-wait since the poll never ran.
			if !opts.NoWait && uploadResult.FailedCount > 0 {
				total := uploadResult.UploadedCount + uploadResult.FailedCount
				if uploadResult.UploadedCount == 0 {
					return cliErrors.WithCode(
						fmt.Sprintf("all %d doc(s) failed ingestion — see warnings above.", total),
						cliErrors.ExitTotalFailure,
					)
				}
				return cliErrors.WithCode(
					fmt.Sprintf("%d of %d doc(s) failed ingestion — see warnings above.", uploadResult.FailedCount, total),
					cliErrors.ExitPartialFailure,
				)
			}
		}

		// Emit JSON result when requested. `usage` was already refreshed
		// post-upload above; if the upload block was skipped (no-op
		// branch), `usage` is still the pre-sync snapshot, which is
		// correct because nothing changed.
		if jsonFlag || saveFlag != "" {
			payload := map[string]any{
				"mode":      opts.JSONMode,
				"dryRun":    opts.DryRun,
				"skipCode":  opts.SkipCode,
				"skipDocs":  opts.SkipDocs,
				"codeFiles": codeFiles,
				"docs":      buildSubmitPayload(docPlan, usage),
			}
			return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
		}
	} else if jsonFlag || saveFlag != "" {
		// skipDocs=true: emit code-only JSON payload.
		// When JSONMode="index" (invoked via workspace index), emit the
		// legacy index shape {mode, workspaceId, codeFiles} for backward
		// compatibility. Otherwise emit the canonical sync shape.
		if opts.JSONMode == "index" {
			payload := map[string]any{
				"mode":        "index",
				"workspaceId": project.WorkspaceID,
				"codeFiles":   codeFiles,
			}
			return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
		}
		payload := map[string]any{
			"mode":      opts.JSONMode,
			"dryRun":    opts.DryRun,
			"skipCode":  opts.SkipCode,
			"skipDocs":  opts.SkipDocs,
			"codeFiles": codeFiles,
		}
		return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
	}

	return nil
}

// registerWorkspaceSync wires "browzer workspace sync" (and its top-level
// alias "browzer sync") under the given parent cobra command.
//
// Calling this twice — once on the workspace subgroup, once on root —
// registers both canonical and alias forms, exactly as registerWorkspaceIndex
// does for "browzer index" / "browzer workspace index".
func registerWorkspaceSync(parent *cobra.Command) {
	var (
		dryRun         bool
		skipCode       bool
		skipDocs       bool
		force          bool
		noWait         bool
		yes            bool
		confirmAdds    int
		confirmDeletes int
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Re-index code + sync existing docs",
		Long: "Re-index both the repository code structure and documents in a single command.\n\n" +
			"Order of operations (always sequential, never reversed):\n" +
			"  1. Code index  — WalkRepo -> POST /api/workspaces/parse (same as: browzer workspace index)\n" +
			"  2. Document sync — reconcile the server's indexed set with local files:\n" +
			"       * local-only (never indexed), passes filter → ADD to workspace\n" +
			"       * already-indexed, changed locally          → re-uploaded\n" +
			"       * already-indexed, deleted locally          → deleted from workspace\n" +
			"       * already-indexed, unchanged                → skipped\n\n" +
			"The stacked ignore filter (.gitignore ∩ .browzerignore) gates the ADD class:\n" +
			"only local docs that pass both filters are eligible for indexing.\n" +
			"Create a .browzerignore at the repo root (same syntax as .gitignore) to opt\n" +
			"specific paths out of automatic indexing without touching .gitignore.\n\n" +
			"Confirmation thresholds (default 50 each) prevent accidental bulk mutations:\n" +
			"  --confirm-adds N      abort when planned ADD set exceeds N (use --yes to bypass)\n" +
			"  --confirm-deletes N   abort when planned DELETE set exceeds N (use --yes to bypass)\n\n" +
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
			"  browzer workspace sync --yes\n" +
			"  browzer workspace sync --confirm-adds 100 --confirm-deletes 20\n" +
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
			return runSyncFlowHook(rootContext(cmd), syncFlowOptions{
				DryRun: dryRun, SkipCode: skipCode, SkipDocs: skipDocs,
				Force: force, NoWait: noWait, Yes: yes,
				ConfirmAdds: confirmAdds, ConfirmDeletes: confirmDeletes,
				JSON: jsonFlag, Save: saveFlag, JSONMode: "sync",
			})
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without applying changes")
	cmd.Flags().BoolVar(&skipCode, "skip-code", false, "Skip the code re-index step (docs only)")
	cmd.Flags().BoolVar(&skipDocs, "skip-docs", false, "Skip the document sync step (code only, same as browzer workspace index)")
	cmd.Flags().BoolVar(&force, "force", false, "Skip the jobs-in-flight preflight and bypass the server's parse gate (X-Force-Parse: true)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Fire-and-forget: enqueue the upload without polling for completion. Exit 0 reflects only the initial POST, not eventual ingestion success or failure. Use `browzer job status <batchId>` to inspect the outcome later.")
	cmd.Flags().BoolVar(&yes, "yes", false, "Bypass --confirm-adds and --confirm-deletes thresholds without prompting.")
	cmd.Flags().IntVar(&confirmAdds, "confirm-adds", 50, "Abort when the planned ADD set exceeds N files unless --yes is passed.")
	cmd.Flags().IntVar(&confirmDeletes, "confirm-deletes", 50, "Abort when the planned DELETE set exceeds N files unless --yes is passed.")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of progress text")
	cmd.Flags().String("save", "", "write JSON to <file> (implies --json)")

	parent.AddCommand(cmd)
}

// ReconcileWorkspaceManifest applies bidirectional last-writer-wins reconciliation
// between the local WorkspaceManifest and the server-fetched workspace list
// (AC-5-cli).
//
// Pull direction (server → local):
//   - Server entry exists, local manifest entry has a different Name/RootPath
//     → update local manifest with server's values (server wins per record).
//   - Manifest entry exists but server list has no matching entry
//     → remove from local manifest (server delete wins).
//
// Push direction (local → server):
//   - Local entries with LocallyModified=true and PendingDelete=true
//     → call client.DeleteWorkspace(ctx, id) then remove from manifest.
//   - Local entries with LocallyModified=true (rename, not delete)
//     → call client.UpdateWorkspace(ctx, id, name, rootPath) then clear the flag.
//
// Returns the modified manifest. The caller is responsible for saving it.
func ReconcileWorkspaceManifest(
	ctx context.Context,
	client WorkspaceSyncClient,
	manifest WorkspaceSyncManifest,
	serverList []api.WorkspaceDto,
) error {
	// Build a set of server IDs for fast lookup.
	serverByID := make(map[string]api.WorkspaceDto, len(serverList))
	for _, ws := range serverList {
		serverByID[ws.ID] = ws
	}

	// Push: flush locally-modified entries to the server before pulling, so
	// that the server's response reflects local intent on the next pull pass.
	for _, local := range manifest.All() {
		if !local.LocallyModified {
			continue
		}
		if local.PendingDelete {
			// Local delete: remove from server then from manifest.
			if err := client.DeleteWorkspace(ctx, local.ID); err != nil {
				return fmt.Errorf("delete workspace %s: %w", local.ID, err)
			}
			manifest.Remove(local.ID)
		} else {
			// Local rename / rootPath change: push to server and clear the flag.
			if err := client.UpdateWorkspace(ctx, local.ID, local.Name, local.RootPath); err != nil {
				return fmt.Errorf("update workspace %s: %w", local.ID, err)
			}
			manifest.Upsert(cache.ManifestEntry{
				ID:              local.ID,
				Name:            local.Name,
				RootPath:        local.RootPath,
				UpdatedAt:       time.Now(),
				LocallyModified: false,
			})
		}
	}

	// Pull: update or remove local entries based on server state.
	for _, local := range manifest.All() {
		srv, exists := serverByID[local.ID]
		if !exists {
			// Server deleted this workspace → remove from manifest.
			manifest.Remove(local.ID)
			continue
		}
		// Server-wins per record: sync name and rootPath if they differ.
		if srv.Name != local.Name || srv.RootPath != local.RootPath {
			manifest.Upsert(cache.ManifestEntry{
				ID:        local.ID,
				Name:      srv.Name,
				RootPath:  srv.RootPath,
				UpdatedAt: time.Now(),
			})
		}
	}

	// Add server entries not yet in the manifest.
	for _, srv := range serverList {
		if _, ok := manifest.Get(srv.ID); !ok {
			manifest.Upsert(cache.ManifestEntry{
				ID:        srv.ID,
				Name:      srv.Name,
				RootPath:  srv.RootPath,
				UpdatedAt: time.Now(),
			})
		}
	}

	return nil
}

// WorkspaceSyncClient is the subset of api.Client used by ReconcileWorkspaceManifest.
// Defined as an interface so tests can inject a mock HTTP server.
type WorkspaceSyncClient interface {
	UpdateWorkspace(ctx context.Context, workspaceID, name, rootPath string) error
	DeleteWorkspace(ctx context.Context, workspaceID string) error
}

// WorkspaceSyncManifest is the subset of cache.WorkspaceManifest used by
// ReconcileWorkspaceManifest. Defined as an interface so tests can inject
// a controlled in-memory manifest without hitting the filesystem.
type WorkspaceSyncManifest interface {
	Get(id string) (cache.ManifestEntry, bool)
	Upsert(entry cache.ManifestEntry)
	Remove(id string)
	All() []cache.ManifestEntry
}

// applySyncSelection adjusts the Selected flag of each DocPickerItem to
// reflect the sync reconciler's intent:
//
//   - server-indexed + no local file  → deselect (DELETE class)
//   - local-only + local file present → select   (ADD class)
//   - all other items                 → unchanged (Selected stays as set by mergeDocItems)
//
// WalkDocs has already applied the .gitignore ∩ .browzerignore filter before
// items are built, so any local-only survivor is eligible for indexing without
// further path checking here.
//
// This is a pure function (no I/O, no globals) so it is unit-testable without
// a network round-trip or a running server.
func applySyncSelection(items []DocPickerItem) []DocPickerItem {
	for i := range items {
		switch {
		case items[i].Indexed && !items[i].HasLocal():
			// File was removed locally → remove from server.
			items[i].Selected = false
		case !items[i].Indexed && items[i].HasLocal():
			// New local file that passes the stacked ignore filter → add to server.
			items[i].Selected = true
		}
		// Indexed + HasLocal: Selected=true (set by mergeDocItems) — keep as-is.
		// !Indexed + !HasLocal: impossible (mergeDocItems never creates ghost-local rows).
	}
	return items
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

// fmtResetAt renders an optional ISO-8601 reset instant as a short,
// human-readable phrase for the daily-ingestion warning. nil means the
// Redis key has no TTL (counter is at zero for today and the bucket
// hasn't been created) — in that case "tomorrow 00:00 UTC" is the
// right expectation but we hedge the phrase as "tomorrow".
func fmtResetAt(t *time.Time) string {
	if t == nil {
		return "tomorrow"
	}
	hours := int(time.Until(*t).Hours())
	if hours <= 0 {
		return "soon"
	}
	if hours < 24 {
		return fmt.Sprintf("in ~%dh", hours)
	}
	return fmt.Sprintf("at %s UTC", t.UTC().Format("2006-01-02 15:04"))
}
