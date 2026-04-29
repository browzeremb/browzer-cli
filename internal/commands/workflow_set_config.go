package commands

import (
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowSetConfig(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "set-config <key> <value>",
		Short:        "Set a config field in workflow.json",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			rawValue := args[1]
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
			return dispatchToDaemonOrFallback(cmd, wfPath, "set-config", wf.MutatorArgs{
				Args: []string{key, rawValue},
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
