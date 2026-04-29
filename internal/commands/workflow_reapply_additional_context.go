package commands

import (
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowReapplyAdditionalContext(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "reapply-additional-context <stepId>",
		Short: "Apply reviewer.additionalContext.changes[] to task.scope in a TASK step",
		Long: `Reads task.reviewer.additionalContext.changes[] from the given TASK step and
applies each change to task.scope:
  - corrected: replaces from→to in scope array
  - added:     appends path to scope (idempotent — skipped if already present)
  - dropped:   removes path from scope

The operation is idempotent: re-running when scope already reflects all changes
exits 0 with a no-op audit line. Closes Phase 2 item #8.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepID := args[0]
			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}
			noLock, _ := cmd.Flags().GetBool("no-lock")
			if !noLock {
				noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
			}
			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}
			return dispatchToDaemonOrFallback(cmd, wfPath, "reapply-additional-context", wf.MutatorArgs{
				Args: []string{stepID},
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
