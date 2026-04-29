package commands

import (
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowAuditModelOverride(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "audit-model-override <stepId> <fromModel> <toModel> <reason>",
		Short: "Record a model override on a TASK step's task.execution.modelOverride field",
		Long: `Writes task.execution.modelOverride: { from, to, reason, at } onto the
specified TASK step. The 'at' timestamp is auto-stamped to now().

Use this when the dispatched model differs from task.suggestedModel to create
an audit trail of model escalations/downgrades. Closes Phase 3 item #9.`,
		Args:         cobra.ExactArgs(4),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepID := args[0]
			fromModel := args[1]
			toModel := args[2]
			reason := args[3]
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
			return dispatchToDaemonOrFallback(cmd, wfPath, "audit-model-override", wf.MutatorArgs{
				Args: []string{stepID, fromModel, toModel, reason},
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
