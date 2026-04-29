package commands

import (
	"fmt"
	"strings"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowCompleteStep(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "complete-step <stepId>",
		Short:        "Transition a step to COMPLETED status in workflow.json",
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

			lock, lockHeld, lockErr := acquireMutatorLock(cmd, wfPath, noLock, lockTimeout)
			if lockErr != nil {
				if lockErr == wf.ErrLockTimeout {
					return errLockTimeoutExitCode
				}
				return lockErr
			}
			if lock != nil {
				defer func() { _ = lock.Release() }()
			}

			_, raw, err := loadWorkflow(wfPath)
			if err != nil {
				return err
			}

			stepMap, _, err := findStepInRaw(raw, stepID)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%v\n", err)
				return err
			}

			// Idempotent: already COMPLETED → warn + return 0.
			if strings.EqualFold(fmt.Sprintf("%v", stepMap["status"]), wf.StatusCompleted) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"warn: step %q is already COMPLETED (idempotent no-op)\n", stepID)
				return nil
			}

			now := time.Now().UTC().Format(time.RFC3339)
			stepMap["status"] = wf.StatusCompleted
			stepMap["completedAt"] = now

			recomputeCounters(raw)

			if err := saveWorkflow(wfPath, raw); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation or write error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=complete-step stepId=%s lockHeldMs=%d validatedOk=true\n",
				stepID, lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
