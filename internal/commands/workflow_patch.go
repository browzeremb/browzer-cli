package commands

import (
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowPatch(parent *cobra.Command) {
	var jqExpr string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "patch",
		Short:        "Apply a jq mutation expression to workflow.json",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jqExpr == "" {
				return fmt.Errorf("--jq is required; provide a jq mutation expression")
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
			return dispatchToDaemonOrFallback(cmd, wfPath, "patch", wf.MutatorArgs{
				JQExpr: jqExpr,
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().StringVar(&jqExpr, "jq", "", "jq expression to apply as mutation (required)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
