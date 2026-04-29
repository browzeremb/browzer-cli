package commands

import (
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// Status transition table is owned by `internal/workflow/apply.go`'s
// `setStatusLegalTransitions`. The historic copy that lived here was deleted
// when the cobra RunE moved to dispatchToDaemonOrFallback — both code paths
// now route through wf.ApplyAndPersist, so there is exactly one source of
// truth for transition legality.

func registerWorkflowSetStatus(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "set-status <stepId> <status>",
		Short:        "Set the lifecycle status of a step in workflow.json",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepID := args[0]
			newStatus := args[1]
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
			return dispatchToDaemonOrFallback(cmd, wfPath, "set-status", wf.MutatorArgs{
				Args: []string{stepID, newStatus},
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
