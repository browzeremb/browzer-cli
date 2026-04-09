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
		dryRun bool
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Interactively (re-)index documents into the workspace",
		Long: `Interactively (re-)index documents into the workspace.

Fetches the currently-indexed documents, walks the local doc tree,
and shows a multi-select picker where already-indexed items come
pre-checked. On submit, the CLI computes a delta:

  • new checked items        → uploaded
  • existing checked changed → re-uploaded
  • existing unchecked       → deleted from the workspace
  • existing checked same    → no-op

Live quota check runs server-side via POST /api/ingestion/preflight
before any upload. If the delta would exceed your plan, the command
exits non-zero BEFORE mutating the workspace.

Requires an interactive TTY. For scripted usage, pair with --yes.

Examples:
  browzer workspace docs
  browzer workspace docs --dry-run
  browzer workspace docs --yes
` + output.ExitCodesHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			if jsonFlag || saveFlag != "" {
				return cliErrors.New("`workspace docs` is interactive and does not support --json/--save.")
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

			if !isTTY() && !yes {
				return cliErrors.New("`workspace docs` requires an interactive terminal. Re-run with --yes to accept the current on-disk state.")
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

			// Phase B/C — build picker and run it (or skip on --yes).
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

			// Apply picker result to items.
			chosen := make(map[int]bool, len(selected))
			for _, idx := range selected {
				chosen[idx] = true
			}
			for i := range items {
				items[i].Selected = chosen[i]
			}

			plan := computeDocDelta(items, docsCache)

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
			// branch never reaches here — guarded above.
			if !yes {
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

			if len(toUpload) > 0 {
				sp := ui.StartSpinner(fmt.Sprintf("Uploading %d docs...", len(toUpload)))
				_, err := upload.UploadInBatches(ctx, client, project.WorkspaceID, toWalkerDocs(toUpload), &newCache, nil, false)
				if err != nil {
					sp.Failure("Upload failed")
					return err
				}
				sp.Success(fmt.Sprintf("Uploaded %d docs", len(toUpload)))
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

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without applying it")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the interactive picker and confirm prompt (accepts local state)")
	cmd.Flags().Bool("json", false, "(unsupported — this command is interactive)")
	cmd.Flags().String("save", "", "(unsupported — this command is interactive)")
	parent.AddCommand(cmd)
}

// fmtMeter renders a "used/limit" pair where limit==0 is displayed as "∞".
func fmtMeter(used, limit int64) string {
	if limit <= 0 {
		return fmt.Sprintf("%d / ∞", used)
	}
	return fmt.Sprintf("%d / %d", used, limit)
}

