package commands

import (
	"errors"
	"fmt"
	"strings"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

func registerJob(parent *cobra.Command) {
	job := &cobra.Command{
		Use:   "job",
		Short: "Inspect async ingestion jobs",
		Long: `Inspect async ingestion batches submitted by ` + "`browzer sync --no-wait`" + `
(and by ` + "`workspace index`" + ` / ` + "`workspace docs`" + ` when they queue work).

Subcommands:
  get <batchId>   Fetch the status of a specific batch.

Tip: poll ` + "`browzer job get <batchId> --json`" + ` until .status is
"completed", "failed", or "partial". ` + "`sync --no-wait`" + ` prints
the batchId on stdout (JSON field "batchId") — capture before polling.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("job requires a subcommand. Try `browzer job get <batchId>` (list from `browzer sync --no-wait`)")
		},
	}

	getCmd := &cobra.Command{
		Use:   "get <batchId>",
		Short: "Fetch the status of an ingestion batch",
		Args:  cobra.ExactArgs(1),
		Long: `Fetch the status of an ingestion batch returned by ` + "`browzer sync --no-wait`" + `.

Single GET against /api/jobs/:batchId — no polling, no retries beyond
the standard idempotent retry. Always emits JSON when --save is set or
when called by an agent (no human form because the schema is too rich).

Examples:
  browzer job get batch-123
  browzer job get batch-123 --save status.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			saveFlag, _ := cmd.Flags().GetString("save")
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			status, err := ac.Client.GetBatchStatus(rootContext(cmd), args[0])
			if err != nil {
				// Map "not found" to exit code 4 for SKILL branching.
				if strings.Contains(strings.ToLower(err.Error()), "not found") || errors.Is(err, ErrJobNotFound) {
					return cliErrors.NotFound("Batch " + args[0])
				}
				return err
			}
			return emitOrFail(status, output.Options{JSON: true, Save: saveFlag}, "")
		},
	}
	getCmd.Flags().String("save", "", "write JSON to <file>")
	job.AddCommand(getCmd)

	parent.AddCommand(job)
}

// ErrJobNotFound is a placeholder sentinel — wired so future api-level
// errors can be classified without string-matching.
var ErrJobNotFound = errors.New("job not found")
