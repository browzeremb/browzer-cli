package commands

import (
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowSetCurrentStep(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "set-current-step <stepId>",
		Short:        "Set the currentStepId in workflow.json and propagate nextStepId",
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

			raw["currentStepId"] = stepID

			// Propagate nextStep from the step to the top-level nextStepId.
			nextStep, _ := stepMap["nextStep"].(string)
			raw["nextStepId"] = nextStep

			raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)

			if err := saveWorkflow(wfPath, raw); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation or write error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=set-current-step stepId=%s lockHeldMs=%d validatedOk=true\n",
				stepID, lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
