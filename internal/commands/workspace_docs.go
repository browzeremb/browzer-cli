// Package commands — `browzer workspace docs`.
//
// Interactive document re-indexer. The legacy `sync` command walked the
// filesystem and blindly applied a cache-based delta, which made it
// impossible for users to opt specific docs in or out. `workspace docs`
// replaces that with an interactive `huh` multi-select that:
//
//   1. Fetches server-side indexed docs, local candidate docs, and live
//      billing quotas concurrently.
//   2. Merges them into a single list where already-indexed items are
//      pre-checked and new items are shown but unchecked by default.
//   3. Lets the user toggle items. On submit we compute a delta
//      (insert / re-upload / delete / keep), call preflight on the
//      insert set, then execute.
//
// Quota enforcement: huh's `Validate` hook on MultiSelect runs only on
// Next/Prev/Submit (see field_multiselect.go:449/456), NOT per-toggle.
// We rely on submit-time validation + server preflight — no attempt to
// reject individual toggles mid-flight.
package commands

import (
	"fmt"
	"sort"
	"strings"

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
	"golang.org/x/sync/errgroup"
)

// DocPickerItem is one row in the interactive picker. It is the merged
// view of "what lives on the filesystem" and "what lives on the
// server", so a single item can represent any combination.
//
// Fields prefixed "Local" come from walker.WalkDocs, "Server" come from
// client.ListWorkspaceDocuments. A fully orphaned server entry (file
// deleted locally but still indexed) has empty Local* fields; a new
// file has empty Server* fields.
type DocPickerItem struct {
	RelativePath string
	LocalHash    string
	LocalSize    int64
	LocalAbs     string

	ServerDocumentID string
	ServerChunkCount int64
	ServerSizeBytes  int64
	ServerStatus     string

	Indexed  bool // server has this path
	Selected bool // user intent after the picker (default = Indexed)
}

// HasLocal reports whether the item exists on the filesystem right now.
func (i DocPickerItem) HasLocal() bool { return i.LocalAbs != "" }

// mergeDocItems joins a local walker.DocFile slice with a server
// []IndexedDocument into a single sorted slice of DocPickerItem. Pure
// function — no I/O, no globals — so it is unit-testable without a TUI
// or a network round trip.
//
// Key is the normalized forward-slash relative path (walker already
// emits those, and the server stores them the same way).
func mergeDocItems(local []walker.DocFile, server []api.IndexedDocument) []DocPickerItem {
	byPath := make(map[string]*DocPickerItem, len(local)+len(server))

	for _, f := range local {
		byPath[f.RelativePath] = &DocPickerItem{
			RelativePath: f.RelativePath,
			LocalHash:    f.SHA256,
			LocalSize:    f.Size,
			LocalAbs:     f.AbsolutePath,
		}
	}
	for _, d := range server {
		item, ok := byPath[d.RelativePath]
		if !ok {
			item = &DocPickerItem{RelativePath: d.RelativePath}
			byPath[d.RelativePath] = item
		}
		item.ServerDocumentID = d.DocumentID
		item.ServerChunkCount = d.ChunkCount
		item.ServerSizeBytes = d.SizeBytes
		item.ServerStatus = d.Status
		item.Indexed = true
		// Default selection mirrors current server state: already-indexed
		// docs come in pre-checked, newly-discovered local docs are OFF.
		item.Selected = true
	}

	out := make([]DocPickerItem, 0, len(byPath))
	for _, v := range byPath {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RelativePath < out[j].RelativePath
	})
	return out
}

// DocDeltaPlan is the outcome of applying a user selection to the
// merged list. It partitions every item into exactly one bucket; the
// four slices together always cover the full input.
type DocDeltaPlan struct {
	// ToInsert are locally-present items the user selected that the
	// server does not yet know about.
	ToInsert []DocPickerItem
	// ToReUpload are selected items the server already has but whose
	// local hash differs from the cached hash (best approximation of
	// "content changed since last sync" — the server never returns a
	// hash).
	ToReUpload []DocPickerItem
	// ToDelete are items the server has but the user deselected (either
	// because the file was deleted locally, or because they explicitly
	// unchecked it).
	ToDelete []DocPickerItem
	// ToKeep are selected items that are unchanged vs the cache — no-op.
	ToKeep []DocPickerItem
}

// computeDocDelta derives the ToInsert/ToReUpload/ToDelete/ToKeep sets
// from a picker result. The cache is used as a proxy for "last known
// server hash" since the server's list endpoint doesn't return hashes.
//
// Invariants:
//   - An item with no local presence cannot be inserted or re-uploaded,
//     only kept (impossible) or deleted (when deselected).
//   - A selected item without server presence always goes to ToInsert.
//   - A selected + indexed + hash-matches-cache item is ToKeep.
//   - A selected + indexed + hash-differs-cache item is ToReUpload.
func computeDocDelta(items []DocPickerItem, docsCache cache.DocsCache) DocDeltaPlan {
	var plan DocDeltaPlan
	for _, it := range items {
		switch {
		case !it.Selected:
			if it.Indexed {
				plan.ToDelete = append(plan.ToDelete, it)
			}
			// !Selected && !Indexed → noise row with no effect.
		case it.Selected && !it.Indexed:
			// Only reachable when the local file exists; the picker
			// doesn't offer "create" for ghost items.
			if it.HasLocal() {
				plan.ToInsert = append(plan.ToInsert, it)
			}
		case it.Selected && it.Indexed:
			if !it.HasLocal() {
				// Server has it but we can't re-upload without bytes;
				// treat as keep (no-op).
				plan.ToKeep = append(plan.ToKeep, it)
				continue
			}
			cached, ok := docsCache.Files[it.RelativePath]
			if ok && cached.SHA256 == it.LocalHash {
				plan.ToKeep = append(plan.ToKeep, it)
			} else {
				plan.ToReUpload = append(plan.ToReUpload, it)
			}
		}
	}
	return plan
}

// toWalkerDocs converts picker items back into walker.DocFile entries
// suitable for upload.UploadInBatches / runPreflight.
func toWalkerDocs(items []DocPickerItem) []walker.DocFile {
	out := make([]walker.DocFile, 0, len(items))
	for _, it := range items {
		if !it.HasLocal() {
			continue
		}
		out = append(out, walker.DocFile{
			RelativePath: it.RelativePath,
			AbsolutePath: it.LocalAbs,
			SHA256:       it.LocalHash,
			Size:         it.LocalSize,
		})
	}
	return out
}

// formatDocOption renders a single picker row label. Already-indexed
// rows get a "(indexed, N chunks)" suffix; new rows get "(new, size)".
func formatDocOption(it DocPickerItem) string {
	if it.Indexed {
		return fmt.Sprintf("%s  (indexed, %d chunks)", it.RelativePath, it.ServerChunkCount)
	}
	return fmt.Sprintf("%s  (new, %s)", it.RelativePath, humanSize(it.LocalSize))
}

// humanSize formats a byte count as a short human-readable string.
func humanSize(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// registerWorkspaceDocs wires the `docs` subcommand under its parent.
func registerWorkspaceDocs(parent *cobra.Command) {
	var (
		dryRun      bool
		yes         bool
		planOnly    bool
		addRaw      string
		removeRaw   string
		replaceRaw  string
		iKnow       bool
		noWait      bool
	)

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "(Re-)index documents (interactive or via flags)",
		Long: `(Re-)index documents into the workspace.

Interactive mode (default, requires TTY):
  Fetches the currently-indexed documents, walks the local doc tree,
  and shows a multi-select picker where already-indexed items come
  pre-checked. On submit, the CLI computes a delta:

    • new checked items        → uploaded
    • existing checked changed → re-uploaded
    • existing unchecked       → deleted from the workspace
    • existing checked same    → no-op

Non-interactive mode (for SKILLs / CI / agents):
  Pass --add, --remove, or --replace with a spec to drive the same
  delta machinery without opening the TUI. Specs support sentinels
  (new/all/none), @file references, stdlib globs (no '**'), and
  comma-separated literal paths.

Live quota check runs server-side via POST /api/ingestion/preflight
before any upload. If the delta would exceed your plan, the command
exits non-zero BEFORE mutating the workspace.

Recipes for common agent prompts:

  User prompt                          → Command
  ─────────────────────────────────────────────────────────────────────
  "Add docs/a.md and docs/b.md"        → browzer workspace docs --add docs/a.md,docs/b.md --yes
  "Remove docs/old.md"                 → browzer workspace docs --remove docs/old.md --yes
  "Index all new docs"                 → browzer workspace docs --add new --yes
  "Replace with just this one doc"     → browzer workspace docs --replace docs/new.md --i-know-what-im-doing --yes
  "Show me what's indexed"             → browzer workspace docs --plan --json
  "Clear all docs from workspace"      → browzer workspace docs --replace none --i-know-what-im-doing --yes

Deletes of 5+ documents require --i-know-what-im-doing.

Examples:
  browzer workspace docs
  browzer workspace docs --dry-run
  browzer workspace docs --yes
  browzer workspace docs --add docs/intro.md,docs/api.md --yes
  browzer workspace docs --remove docs/old.md --yes
  browzer workspace docs --add 'docs/*.md' --yes
  browzer workspace docs --add @paths.txt --yes
  browzer workspace docs --plan --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			if saveFlag != "" {
				jsonFlag = true
			}

			// --- Flag mutual-exclusion validation (early return block).
			//
			// We gate the whole non-interactive surface here so the
			// main RunE body below can assume "at most one mutation
			// mode is active" and "--plan means read-only".
			hasAdd := addRaw != ""
			hasRemove := removeRaw != ""
			hasReplace := replaceRaw != ""
			nonInteractive := hasAdd || hasRemove || hasReplace
			if planOnly {
				if nonInteractive || yes || dryRun || iKnow {
					return cliErrors.New("--plan is read-only and cannot be combined with selection/mutation flags.")
				}
			}
			if hasReplace && (hasAdd || hasRemove) {
				return cliErrors.New("--replace cannot be combined with --add or --remove.")
			}
			// Legacy contract: without --plan and without any
			// selection flag, --json/--save are still unsupported
			// because the default path is the interactive TUI.
			if !planOnly && !nonInteractive && (jsonFlag || saveFlag != "") {
				return cliErrors.New("`workspace docs` is interactive and does not support --json/--save without --plan or --add/--remove/--replace.")
			}

			// Parse specs eagerly so errors come out before we touch
			// the network.
			var addSpec, removeSpec, replaceSpec *SpecResolver
			if hasAdd {
				s, err := parseSpec(addRaw, SpecScopeAdd)
				if err != nil {
					return cliErrors.Newf("--add: %v", err)
				}
				addSpec = s
			}
			if hasRemove {
				s, err := parseSpec(removeRaw, SpecScopeRemove)
				if err != nil {
					return cliErrors.Newf("--remove: %v", err)
				}
				removeSpec = s
			}
			if hasReplace {
				s, err := parseSpec(replaceRaw, SpecScopeReplace)
				if err != nil {
					return cliErrors.Newf("--replace: %v", err)
				}
				replaceSpec = s
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

			// TTY guard: the default interactive path requires a
			// terminal. Non-interactive mode (--plan or any
			// selection flag) never needs one.
			if !isTTY() && !yes && !planOnly && !nonInteractive {
				return cliErrors.New("`workspace docs` requires an interactive terminal. Re-run with --yes to accept the current on-disk state, or use --add/--remove/--replace/--plan for non-interactive mode.")
			}

			ctx := rootContext(cmd)
			printColdStartHint(false)
			ac, err := requireAuth(600)
			if err != nil {
				return err
			}
			client := ac.Client

			ui.Arrow(fmt.Sprintf("Workspace: %s", project.WorkspaceID))

			// Phase A — three independent fetches in parallel.
			var (
				serverDocs []api.IndexedDocument
				localDocs  []walker.DocFile
				usage      *api.BillingUsageResponse
			)
			sp := ui.StartSpinner("Loading workspace state...")
			g, gctx := errgroup.WithContext(ctx)
			g.Go(func() error {
				docs, err := client.ListWorkspaceDocuments(gctx, project.WorkspaceID)
				if err != nil {
					return fmt.Errorf("list workspace documents: %w", err)
				}
				serverDocs = docs
				return nil
			})
			g.Go(func() error {
				docs, err := walker.WalkDocs(gitRoot)
				if err != nil {
					return fmt.Errorf("walk local docs: %w", err)
				}
				localDocs = docs
				return nil
			})
			g.Go(func() error {
				u, err := client.BillingUsage(gctx)
				if err != nil {
					// Quota is nice-to-have — a fetch failure shouldn't
					// block the picker. Emit a warning and continue.
					ui.Warn(fmt.Sprintf("could not fetch billing usage: %v", err))
					return nil
				}
				usage = u
				return nil
			})
			if err := g.Wait(); err != nil {
				sp.Failure("Load failed")
				return err
			}
			sp.Success(fmt.Sprintf("Loaded %d server doc(s), %d local candidate(s)", len(serverDocs), len(localDocs)))

			items := mergeDocItems(localDocs, serverDocs)
			if len(items) == 0 {
				ui.Info("No documents to manage.")
				return nil
			}

			docsCache := cache.Load(gitRoot)

			// --- Read-only plan mode: emit the merged state + quota
			// and exit. No mutations, no confirmation.
			if planOnly {
				return emitDocsPlan(items, usage, output.Options{JSON: jsonFlag, Save: saveFlag})
			}

			// Phase B/C — either run the TUI picker, or resolve the
			// non-interactive spec into items[].Selected.
			if nonInteractive {
				res, updated := applySpecsToItems(items, addSpec, removeSpec, replaceSpec)
				items = updated
				// --add / --replace: unresolved literal paths are a
				// hard error — the user asked for something that
				// doesn't exist in the candidate set.
				if len(res.UnresolvedAdd) > 0 {
					return cliErrors.Newf("--add: paths not found in workspace candidates: %s", strings.Join(res.UnresolvedAdd, ", "))
				}
				if len(res.UnresolvedReplace) > 0 {
					return cliErrors.Newf("--replace: paths not found in workspace candidates: %s", strings.Join(res.UnresolvedReplace, ", "))
				}
				// --remove: not-currently-indexed is a warning, not
				// a fatal error — agents chain --remove calls and
				// shouldn't break on already-gone docs.
				for _, p := range res.UnresolvedRemove {
					output.Errf("warning: --remove: %q is not currently indexed, skipping\n", p)
				}
			} else {
				selected := make([]int, 0, len(items))
				for i, it := range items {
					if it.Selected {
						selected = append(selected, i)
					}
				}
				if !yes {
					options := make([]huh.Option[int], len(items))
					for i, it := range items {
						options[i] = huh.NewOption(formatDocOption(it), i)
					}
					ms := huh.NewMultiSelect[int]().
						Title("Pick documents to keep in the workspace").
						Description("Toggle with space, submit with enter. Checked items are kept; unchecked indexed items are deleted.").
						Options(options...).
						Value(&selected).
						Height(20)
					if err := ms.Run(); err != nil {
						return fmt.Errorf("picker: %w", err)
					}
				}
				chosen := make(map[int]bool, len(selected))
				for _, idx := range selected {
					chosen[idx] = true
				}
				for i := range items {
					items[i].Selected = chosen[i]
				}
			}

			deltaPlan := computeDocDelta(items, docsCache)

			// --- Safeguard: large-delete confirmation.
			if len(deltaPlan.ToDelete) >= 5 && !iKnow {
				return cliErrors.New(largeDeleteMessage(deltaPlan))
			}
			// --- Safeguard: non-TTY shells must pass --yes for any
			// mutation even in non-interactive mode (we refuse the
			// "stdin isn't a TTY and you didn't confirm" combo).
			if (!isTTY() || nonInteractive) && !yes {
				if len(deltaPlan.ToInsert) > 0 || len(deltaPlan.ToReUpload) > 0 || len(deltaPlan.ToDelete) > 0 {
					if !dryRun {
						return cliErrors.New("Non-interactive shells require --yes to submit mutations.")
					}
				}
			}

			// Rebind to `plan` so the downstream legacy code (which
			// historically named the delta `plan`) still compiles
			// unchanged.
			plan := deltaPlan

			summary := fmt.Sprintf(
				"Plan: +%d insert, ~%d re-upload, -%d delete, =%d keep",
				len(plan.ToInsert), len(plan.ToReUpload), len(plan.ToDelete), len(plan.ToKeep),
			)
			ui.Arrow(summary)

			if dryRun {
				fmt.Println("Dry run — no changes applied.")
				return nil
			}

			if len(plan.ToInsert) == 0 && len(plan.ToReUpload) == 0 && len(plan.ToDelete) == 0 {
				// Edge case: workspace is empty on the server AND we
				// found local candidates, yet nothing made it into the
				// plan. The most common cause is a non-interactive
				// --yes run that didn't pass --add/--replace — "sync"
				// with no selection is a no-op by design, but staying
				// silent hides the obvious next step from an agent.
				if len(serverDocs) == 0 && len(localDocs) > 0 && !nonInteractive && yes {
					ui.Warn(fmt.Sprintf(
						"Workspace is empty and %d local candidate(s) found. Use 'browzer workspace docs --add new --yes' to index all, or --add <paths> for a subset.",
						len(localDocs),
					))
					return nil
				}
				ui.Success("Workspace documents already in sync.")
				return nil
			}

			// Phase D — preflight authoritative check, then execute.
			toUpload := append([]DocPickerItem{}, plan.ToInsert...)
			toUpload = append(toUpload, plan.ToReUpload...)
			if len(toUpload) > 0 {
				if err := runPreflight(ctx, client, toWalkerDocs(toUpload)); err != nil {
					return err
				}
			}

			// Interactive confirm (skippable with --yes). Non-TTY
			// and non-interactive mode never reach here — guarded
			// above or skipped explicitly.
			if !yes && !nonInteractive {
				confirm := false
				err := huh.NewConfirm().
					Title(summary).
					Description("Apply these changes to the workspace?").
					Affirmative("Apply").
					Negative("Cancel").
					Value(&confirm).
					Run()
				if err != nil {
					return err
				}
				if !confirm {
					ui.Warn("Cancelled.")
					return nil
				}
			}

			// Execute: uploads first, then deletes. Cache mirrors the
			// legacy sync.go invariant: merge existing entries so we
			// don't drop unrelated keys.
			newCache := cache.DocsCache{
				Version: cache.CacheVersion,
				Files:   make(map[string]cache.CachedDoc, len(docsCache.Files)),
			}
			for k, v := range docsCache.Files {
				newCache.Files[k] = v
			}

			var uploadResult upload.Result
			if len(toUpload) > 0 {
				sp := ui.StartSpinner(fmt.Sprintf("Uploading %d docs...", len(toUpload)))
				r, err := upload.UploadInBatches(ctx, client, &project.WorkspaceID, toWalkerDocs(toUpload), &newCache, nil, noWait)
				if err != nil {
					sp.Failure("Upload failed")
					return err
				}
				uploadResult = r
				if noWait {
					// --no-wait: exit 0 reflects only the initial POST, not
					// eventual completion. Per-file ingestion results are
					// not yet known. Use `browzer job status <batchId>` to
					// inspect the outcome later.
					sp.Success(fmt.Sprintf("Enqueued %d docs (batch IDs: %s)", len(toUpload), strings.Join(r.BatchIDs, ", ")))
				} else if r.FailedCount > 0 {
					// Default (polling) path: some docs failed ingestion.
					// Print the per-file summary and mark the spinner as failed.
					sp.Failure(fmt.Sprintf("⚠ %d of %d docs failed", r.FailedCount, len(toUpload)))
					for _, name := range r.FailedNames {
						output.Errf("  ⚠ %s: ingestion failed\n", name)
					}
				} else {
					// All docs completed successfully — defer this headline
					// until AFTER the poll returns so the timing is truthful.
					sp.Success(fmt.Sprintf("✓ Uploaded %d docs", r.UploadedCount))
				}
			}

			deletesFailed := 0
			var failedDeletes []string
			if len(plan.ToDelete) > 0 {
				sp := ui.StartSpinner(fmt.Sprintf("Deleting %d docs...", len(plan.ToDelete)))
				for _, d := range plan.ToDelete {
					if err := client.DeleteDocument(ctx, d.ServerDocumentID, project.WorkspaceID); err != nil {
						deletesFailed++
						failedDeletes = append(failedDeletes, fmt.Sprintf("%s: %v", d.RelativePath, err))
					} else {
						delete(newCache.Files, d.RelativePath)
					}
				}
				if deletesFailed == 0 {
					sp.Success(fmt.Sprintf("Deleted %d docs", len(plan.ToDelete)))
				} else {
					sp.Failure(fmt.Sprintf("Deleted %d/%d docs", len(plan.ToDelete)-deletesFailed, len(plan.ToDelete)))
					for _, f := range failedDeletes {
						ui.Warn(f)
					}
				}
			}

			if err := cache.Save(gitRoot, newCache); err != nil {
				return err
			}

			if deletesFailed > 0 {
				return cliErrors.Newf("%d document delete(s) failed — see warnings above.", deletesFailed)
			}

			// Exit-code taxonomy for ingestion poll failures (default path only;
			// --no-wait skips the poll so exit 0 reflects only the initial POST):
			//   7 (ExitPartialFailure) — some docs failed, at least one succeeded
			//   8 (ExitTotalFailure)   — all docs failed (no completions)
			if !noWait && uploadResult.FailedCount > 0 {
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

			// --- Non-interactive submit: emit machine-readable result.
			if nonInteractive && jsonFlag {
				// Re-fetch quota so the "after" snapshot reflects the
				// mutation we just applied. Errors are tolerated — we
				// fall back to the pre-submit usage snapshot.
				after := usage
				if u, uerr := client.BillingUsage(ctx); uerr == nil {
					after = u
				}
				payload := buildSubmitPayload(plan, after)
				return output.Emit(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
			}

			fmt.Println()
			ui.Success("Documents re-indexed")

			// Show the post-run quota snapshot when we managed to fetch it.
			if usage != nil {
				fmt.Printf("  Chunks:  %s\n", fmtMeter(usage.Chunks.Used, usage.Chunks.Limit))
				fmt.Printf("  Storage: %s / %s\n",
					humanSize(usage.Storage.Used), humanSize(usage.Storage.Limit))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without applying it. Example: browzer workspace docs --add docs/a.md --dry-run")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the interactive picker and confirm prompt. Required by non-interactive mutations. Example: browzer workspace docs --yes")
	cmd.Flags().BoolVar(&planOnly, "plan", false, "Read-only: emit the current merge plan (server docs + local candidates + quota) and exit. Example: browzer workspace docs --plan --json")
	cmd.Flags().StringVar(&addRaw, "add", "", "Non-interactive: add paths matching <spec>. Supports 'new' sentinel, @file refs, globs, or comma lists. Example: --add docs/a.md,docs/b.md  OR  --add new  OR  --add 'docs/*.md'")
	cmd.Flags().StringVar(&removeRaw, "remove", "", "Non-interactive: remove indexed paths matching <spec>. Example: --remove docs/old.md  OR  --remove 'legacy/*.md'")
	cmd.Flags().StringVar(&replaceRaw, "replace", "", "Non-interactive: replace the full selection with <spec>. Sentinels: 'all' (every local file) / 'none' (delete everything). Example: --replace docs/only.md --i-know-what-im-doing")
	cmd.Flags().BoolVar(&iKnow, "i-know-what-im-doing", false, "Confirm destructive intent — required when the computed delta would delete >= 5 documents.")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Fire-and-forget: enqueue the upload without polling for completion. Exit 0 reflects only the initial POST, not eventual ingestion success or failure. Use `browzer job status <batchId>` to inspect the outcome later.")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON. Works with --plan (read-only) and with --add/--remove/--replace (submit result).")
	cmd.Flags().String("save", "", "Write JSON output to the given file path. Implies --json. Example: --save /tmp/docs-plan.json")
	parent.AddCommand(cmd)
}

// docsPlanItemJSON is the per-row shape emitted by --plan. Fields
// mirror the CLAUDE.md spec exactly; rename-sensitive, don't edit
// without also updating the SKILL JSON example.
type docsPlanItemJSON struct {
	Path             string `json:"path"`
	Indexed          bool   `json:"indexed"`
	LocalHash        string `json:"localHash,omitempty"`
	LocalSize        int64  `json:"localSize,omitempty"`
	ServerDocumentID string `json:"serverDocumentId,omitempty"`
	ServerChunks     int64  `json:"serverChunks,omitempty"`
	ServerBytes      int64  `json:"serverBytes,omitempty"`
	Status           string `json:"status,omitempty"`
}

// docsQuotaJSON is the quota block shared by --plan and submit
// payloads. Workspaces, Chunks, Storage are `BillingCounter`-shaped.
type docsQuotaJSON struct {
	Plan       string                 `json:"plan"`
	Storage    map[string]int64       `json:"storage"`
	Chunks     map[string]int64       `json:"chunks"`
	Workspaces map[string]int64       `json:"workspaces"`
}

// docsPlanJSON is the full --plan payload.
type docsPlanJSON struct {
	Items []docsPlanItemJSON `json:"items"`
	Quota *docsQuotaJSON     `json:"quota,omitempty"`
}

// docsSubmitEntryJSON represents one row in the submit lists.
type docsSubmitEntryJSON struct {
	Path       string `json:"path"`
	DocumentID string `json:"documentId,omitempty"`
	Chunks     int64  `json:"chunks,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// docsSubmitJSON is the full submit payload. All lists are
// always-present arrays (never null) so agent parsers can rely on
// `.inserted | length` without null-checks.
type docsSubmitJSON struct {
	Inserted   []docsSubmitEntryJSON `json:"inserted"`
	Reuploaded []docsSubmitEntryJSON `json:"reuploaded"`
	Deleted    []docsSubmitEntryJSON `json:"deleted"`
	Skipped    []docsSubmitEntryJSON `json:"skipped"`
	QuotaAfter *docsQuotaJSON        `json:"quotaAfter,omitempty"`
}

// buildQuotaJSON converts a BillingUsageResponse into the shared
// quota-block shape. Returns nil when usage is nil so the JSON omits
// the whole block rather than emitting empty counters.
func buildQuotaJSON(u *api.BillingUsageResponse) *docsQuotaJSON {
	if u == nil {
		return nil
	}
	return &docsQuotaJSON{
		Plan:       u.Plan,
		Storage:    map[string]int64{"used": u.Storage.Used, "limit": u.Storage.Limit},
		Chunks:     map[string]int64{"used": u.Chunks.Used, "limit": u.Chunks.Limit},
		Workspaces: map[string]int64{"used": u.Workspaces.Used, "limit": u.Workspaces.Limit},
	}
}

// emitDocsPlan serializes the read-only merge state for --plan. The
// human fallback prints a compact table so `--plan` without --json is
// still useful at the terminal.
func emitDocsPlan(items []DocPickerItem, usage *api.BillingUsageResponse, opts output.Options) error {
	payload := docsPlanJSON{
		Items: make([]docsPlanItemJSON, 0, len(items)),
		Quota: buildQuotaJSON(usage),
	}
	for _, it := range items {
		payload.Items = append(payload.Items, docsPlanItemJSON{
			Path:             it.RelativePath,
			Indexed:          it.Indexed,
			LocalHash:        it.LocalHash,
			LocalSize:        it.LocalSize,
			ServerDocumentID: it.ServerDocumentID,
			ServerChunks:     it.ServerChunkCount,
			ServerBytes:      it.ServerSizeBytes,
			Status:           it.ServerStatus,
		})
	}
	var human strings.Builder
	if !opts.JSON && opts.Save == "" {
		fmt.Fprintf(&human, "Workspace documents (%d):\n", len(items))
		for _, it := range items {
			state := "new"
			if it.Indexed {
				state = "indexed"
			}
			fmt.Fprintf(&human, "  %-40s  %-8s  %s\n", it.RelativePath, state, humanSize(it.LocalSize))
		}
		if usage != nil {
			fmt.Fprintf(&human, "\nQuota (%s):\n", usage.Plan)
			fmt.Fprintf(&human, "  Chunks:  %s\n", fmtMeter(usage.Chunks.Used, usage.Chunks.Limit))
			fmt.Fprintf(&human, "  Storage: %s / %s\n", humanSize(usage.Storage.Used), humanSize(usage.Storage.Limit))
		}
	}
	return output.Emit(payload, opts, human.String())
}

// buildSubmitPayload renders the plan into the submit JSON shape. We
// don't have per-file success/failure granularity from
// upload.UploadInBatches, so we treat the whole plan as applied (the
// caller only reaches this path when upload + delete returned nil).
func buildSubmitPayload(plan DocDeltaPlan, usageAfter *api.BillingUsageResponse) docsSubmitJSON {
	out := docsSubmitJSON{
		Inserted:   []docsSubmitEntryJSON{},
		Reuploaded: []docsSubmitEntryJSON{},
		Deleted:    []docsSubmitEntryJSON{},
		Skipped:    []docsSubmitEntryJSON{},
		QuotaAfter: buildQuotaJSON(usageAfter),
	}
	for _, it := range plan.ToInsert {
		out.Inserted = append(out.Inserted, docsSubmitEntryJSON{Path: it.RelativePath})
	}
	for _, it := range plan.ToReUpload {
		out.Reuploaded = append(out.Reuploaded, docsSubmitEntryJSON{
			Path:       it.RelativePath,
			DocumentID: it.ServerDocumentID,
			Chunks:     it.ServerChunkCount,
		})
	}
	for _, it := range plan.ToDelete {
		out.Deleted = append(out.Deleted, docsSubmitEntryJSON{
			Path:       it.RelativePath,
			DocumentID: it.ServerDocumentID,
		})
	}
	for _, it := range plan.ToKeep {
		out.Skipped = append(out.Skipped, docsSubmitEntryJSON{
			Path:   it.RelativePath,
			Reason: "already indexed, hash unchanged",
		})
	}
	return out
}

// defaultDocsCache returns an empty-but-valid DocsCache so tests (and
// fallback paths) can feed computeDocDelta without touching disk.
func defaultDocsCache() cache.DocsCache {
	return cache.DocsCache{Version: cache.CacheVersion, Files: map[string]cache.CachedDoc{}}
}

// largeDeleteMessage renders the refusal text shown when a delta
// contains 5+ deletes and --i-know-what-im-doing was not passed. Pure
// function so we can pin the exact wording in unit tests.
func largeDeleteMessage(plan DocDeltaPlan) string {
	var paths []string
	for _, d := range plan.ToDelete {
		paths = append(paths, "  "+d.RelativePath)
	}
	return fmt.Sprintf(
		"this operation would delete %d indexed documents.\nDeletes >= 5 require the --i-know-what-im-doing flag to confirm\nthe destructive intent. Documents that would be deleted:\n\n%s\n\nRe-run with --i-know-what-im-doing to proceed.",
		len(plan.ToDelete), strings.Join(paths, "\n"),
	)
}

// fmtMeter renders a "used/limit" pair where limit==0 is displayed as "∞".
func fmtMeter(used, limit int64) string {
	if limit <= 0 {
		return fmt.Sprintf("%d / ∞", used)
	}
	return fmt.Sprintf("%d / %d", used, limit)
}

