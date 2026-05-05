package commands

import (
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// registerWorkflowAppendSteps wires `browzer workflow append-steps` — the
// plural sibling of `append-step`. The payload is a JSON array of step
// objects; the daemon (or standalone fallback) acquires the advisory lock
// once, applies all entries against the same in-memory map, validates the
// final document against CUE once, and persists via a single tmp+rename.
//
// Closes RETRO 9.1.5 from the 2026-05-05 orchestrate-task-delivery session:
// an 11-task TASKS_MANIFEST persisted via 11 sequential `append-step`
// invocations paid 11 advisory-lock acquisitions + 11 CUE-validations.
// `append-steps` collapses that to one of each.
func registerWorkflowAppendSteps(parent *cobra.Command) {
	var payloadFile string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "append-steps",
		Short: "Append N steps to workflow.json under a single advisory lock",
		Long: `Append a JSON array of step objects to workflow.json in one mutation.

Payload shape:

  [
    { "stepId": "STEP_03_TASK_01", "name": "TASK", ... },
    { "stepId": "STEP_04_TASK_02", "name": "TASK", ... },
    ...
  ]

The payload is read from stdin by default, or from a file via --payload.
The CUE schema is validated ONCE against the post-mutation document — the
caller is therefore safe to use intermediate states that wouldn't pass
validation in isolation, as long as the final document is valid. The
advisory lock is acquired ONCE for the whole batch and the audit line
emitted ONCE on success (mode=daemon-async / daemon-sync / fallback-sync
depending on the dispatch path).

For a single step, prefer the singular ` + "`append-step`" + ` — the plural
verb is for batches.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			noLock, _ := cmd.Flags().GetBool("no-lock")
			if !noLock {
				noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
			}

			payloadBytes, err := readPayload(cmd, payloadFile)
			if err != nil {
				return fmt.Errorf("read payload: %w", err)
			}

			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}
			return dispatchToDaemonOrFallback(cmd, wfPath, "append-steps", wf.MutatorArgs{
				Payload: payloadBytes,
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().StringVar(&payloadFile, "payload", "", "path to JSON-array payload file (reads from stdin if omitted)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
