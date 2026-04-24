// Package commands — `browzer workspace index`.
//
// Thin alias for `browzer sync --skip-docs`. Delegates to runSyncFlow
// with SkipDocs=true and JSONMode="index" (backward-compat wire shape).
//
// Retained behaviors:
//   - cold-start 600s auth timeout (via runSyncFlow → requireAuth(600)).
//   - --dry-run / --json / --save plumbing forwarded into syncFlowOptions.
//   - Stamps ProjectConfig.LastSyncCommit on success (inside runSyncFlow).
//   - JSON payload shape preserved: {mode:"index", workspaceId, codeFiles}.
package commands

import (
	"context"
	"fmt"

	"github.com/browzeremb/browzer-cli/internal/api"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// registerWorkspaceIndex wires `browzer workspace index` under the given
// parent cobra command. It is called from both the top-level root (as
// the `browzer index` alias) and the `workspace` subgroup.
func registerWorkspaceIndex(parent *cobra.Command) {
	var (
		dryRun bool
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Re-index code into workspace graph",
		Long: `Re-parse the repository's code structure into the workspace graph.

This is the cheap command: it walks the repo tree and hands the
folders/files/symbols to the server's regex parser. It does NOT
touch documents, does NOT consume chunk quota, and does NOT embed
anything.

Equivalent to: browzer sync --skip-docs

To re-index markdown, PDFs and other documents, use:

  browzer workspace docs

Examples:
  browzer workspace index
  browzer workspace index --dry-run
  browzer workspace index --json --save index.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")
			if saveFlag != "" {
				jsonFlag = true
			}
			return runSyncFlowHook(rootContext(cmd), syncFlowOptions{
				DryRun:         dryRun,
				SkipDocs:       true,
				Force:          force,
				NoWait:         false,
				Yes:            false,
				ConfirmAdds:    50,
				ConfirmDeletes: 50,
				JSON:           jsonFlag,
				Save:           saveFlag,
				JSONMode:       "index",
			})
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would happen without calling the server")
	cmd.Flags().BoolVar(&force, "force", false, "Skip the jobs-in-flight preflight and bypass the server's parse gate (X-Force-Parse: true)")
	cmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of progress text")
	cmd.Flags().String("save", "", "write JSON to <file> (implies --json)")
	parent.AddCommand(cmd)
}

// preflightJobsInFlight is the shared Fase 0 check used by `index` and
// `sync` (PR 3). Returns nil to proceed; non-nil error aborts the
// command with a friendly exit code.
//
//   - 0 jobs in flight       → nil (proceed).
//   - jobs in flight + TTY   → interactive confirm, N → ErrAbort sentinel.
//   - jobs in flight + non-TTY → structured ERR_JOBS_IN_FLIGHT error.
//
// A failure of the preflight HTTP call itself is logged as a warning
// and the function returns nil — abuse of the gate must not be the only
// thing standing between a user and a parse, and the server-side gate
// will catch real races anyway.
func preflightJobsInFlight(ctx context.Context, client *api.Client, workspaceID string, quiet bool) error {
	jobs, err := client.GetWorkspaceJobs(ctx, workspaceID)
	if err != nil {
		// Older server (no /jobs route) or transient blip — don't block.
		// Server-side gate still catches real races.
		if !quiet {
			ui.Warn(fmt.Sprintf("could not check jobs-in-flight (continuing): %v", err))
		}
		return nil
	}
	total := jobs.Pending + jobs.Processing
	if total == 0 {
		return nil
	}

	msg := fmt.Sprintf(
		"%d ingestion job(s) still in flight (%d pending, %d processing).",
		total, jobs.Pending, jobs.Processing,
	)
	if jobs.OldestEnqueuedAt != "" {
		msg += " Oldest enqueued at " + jobs.OldestEnqueuedAt + "."
	}

	if !isTTY() || quiet {
		// Non-interactive: surface a structured error so scripts can
		// react. `--force` bypasses; mention it in the message.
		return cliErrors.Newf("%s Re-run with --force to bypass.", msg)
	}

	confirm := false
	if confirmErr := huh.NewConfirm().
		Title(msg).
		Description("Proceed with re-parse anyway?").
		Affirmative("Proceed").
		Negative("Cancel").
		Value(&confirm).
		Run(); confirmErr != nil {
		return confirmErr
	}
	if !confirm {
		ui.Warn("Cancelled.")
		return cliErrors.New("aborted by user")
	}
	return nil
}
