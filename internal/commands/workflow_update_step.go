package commands

import (
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowUpdateStep(parent *cobra.Command) {
	var setPairs []string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "update-step <stepId>",
		Short:        "Update fields on a named step in workflow.json",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepID := args[0]

			if len(setPairs) == 0 {
				return fmt.Errorf("--set is required; provide at least one field=value pair")
			}

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
			// Pack stepId + field=value pairs into args; mutatorUpdateStep
			// expects args[0]=stepId, args[1..]=pairs.
			mArgs := wf.MutatorArgs{
				Args: append([]string{stepID}, setPairs...),
			}
			return dispatchToDaemonOrFallback(cmd, wfPath, "update-step", mArgs, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().StringArrayVar(&setPairs, "set", nil, "field=value pair to set on the step (repeatable)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
