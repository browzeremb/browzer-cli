// Package commands — `browzer org docs add`.
//
// Uploads documents to org-scope (workspaceId=null). Unlike `workspace docs`,
// this command does NOT require a git root, a .browzer/config.json, or a
// local cache. Each invocation is stateless — pass explicit file paths and
// they are uploaded as org-scoped documents that are not bound to any
// workspace.
package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/browzeremb/browzer-cli/internal/cache"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/browzeremb/browzer-cli/internal/upload"
	"github.com/browzeremb/browzer-cli/internal/walker"
	"github.com/spf13/cobra"
)

func registerOrgDocsAdd(parent *cobra.Command) {
	var (
		yes     bool
		dryRun  bool
		jsonOut bool
	)

	cmd := &cobra.Command{
		Use:   "add [paths...]",
		Short: "Upload org-scoped docs (not bound to any workspace)",
		Long: `Upload one or more Markdown files as org-scoped documents.

Org-scoped documents are not attached to any workspace. They are searchable
across the whole organization and useful for policy docs, shared references,
or any content that should not live inside a specific workspace.

No git root, no .browzer/config.json, and no local cache are required.
Pass file paths directly as arguments.

Examples:
  browzer org docs add docs/policy.md docs/onboarding.md --yes
  browzer org docs add docs/policy.md --dry-run
  browzer org docs add docs/policy.md --yes --json
` + output.ExitCodesHelp,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")

			// Resolve paths and build walker.DocFile entries manually.
			// No git-root detection — paths are taken as-is.
			var docs []walker.DocFile
			for _, p := range args {
				abs, err := filepath.Abs(p)
				if err != nil {
					return fmt.Errorf("resolve path %q: %w", p, err)
				}
				info, err := os.Stat(abs)
				if err != nil {
					return fmt.Errorf("stat %q: %w", p, err)
				}
				if info.IsDir() {
					return fmt.Errorf("%q is a directory — pass individual file paths", p)
				}

				// Compute SHA-256 so runPreflight can estimate byte size.
				data, err := os.ReadFile(abs)
				if err != nil {
					return fmt.Errorf("read %q: %w", p, err)
				}
				sum := sha256.Sum256(data)
				hash := hex.EncodeToString(sum[:])

				// Use the filename as the relative path (org-scope has no
				// shared root directory by definition).
				docs = append(docs, walker.DocFile{
					RelativePath: filepath.Base(abs),
					AbsolutePath: abs,
					SHA256:       hash,
					Size:         info.Size(),
				})
			}

			if len(docs) == 0 {
				ui.Info("No documents to upload.")
				return nil
			}

			// Print preview regardless of --dry-run so the user knows what
			// will happen before confirming.
			ui.Arrow(fmt.Sprintf("Org-scoped upload: %d file(s)", len(docs)))
			for _, d := range docs {
				fmt.Printf("  %s  (%s)\n", d.RelativePath, humanSize(d.Size))
			}

			if dryRun {
				fmt.Println("Dry run — no changes applied.")
				return nil
			}

			ctx := rootContext(cmd)
			printColdStartHint(false)
			ac, err := requireAuth(600)
			if err != nil {
				return err
			}
			client := ac.Client

			// Preflight: server-side quota check. runPreflight does not send
			// workspaceId — it is already compatible with org-scope.
			if err := runPreflight(ctx, client, docs); err != nil {
				return err
			}

			if !yes && isTTY() {
				// Simple y/N prompt — no huh dependency needed for this path.
				fmt.Printf("Upload %d org-scoped document(s)? [y/N] ", len(docs))
				var answer string
				_, _ = fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" {
					ui.Warn("Cancelled.")
					return nil
				}
			}

			sp := ui.StartSpinner(fmt.Sprintf("Uploading %d org-scoped doc(s)...", len(docs)))
			// nil workspaceID → server creates Document with workspaceId=null (org-scope).
			emptyCache := cache.DocsCache{
				Version: cache.CacheVersion,
				Files:   map[string]cache.CachedDoc{},
			}
			result, err := upload.UploadInBatches(ctx, client, nil, docs, &emptyCache, nil, false)
			if err != nil {
				sp.Failure("Upload failed")
				return err
			}
			sp.Success(fmt.Sprintf("Uploaded %d doc(s) (%d failed)", result.UploadedCount, result.FailedCount))

			if jsonFlag {
				type addResult struct {
					Uploaded int `json:"uploaded"`
					Failed   int `json:"failed"`
				}
				return output.Emit(addResult{
					Uploaded: result.UploadedCount,
					Failed:   result.FailedCount,
				}, output.Options{JSON: true}, "")
			}

			if result.FailedCount > 0 {
				return fmt.Errorf("%d document(s) failed — see warnings above", result.FailedCount)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the list of files without uploading")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON result")
	_ = jsonOut // accessed via cmd.Flags().GetBool("json") inside RunE
	parent.AddCommand(cmd)
}
