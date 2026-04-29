package commands

import (
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowAppendStep(parent *cobra.Command) {
	var payloadFile string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "append-step",
		Short:        "Append a step to workflow.json",
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
			return dispatchToDaemonOrFallback(cmd, wfPath, "append-step", wf.MutatorArgs{
				Payload: payloadBytes,
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().StringVar(&payloadFile, "payload", "", "path to step JSON payload file (reads from stdin if omitted)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
