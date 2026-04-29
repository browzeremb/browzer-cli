package commands

import (
	"fmt"
	"strings"
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

			// Apply each field=value pair.
			for _, pair := range setPairs {
				idx := strings.IndexByte(pair, '=')
				if idx < 0 {
					return fmt.Errorf("invalid --set value %q: expected field=value", pair)
				}
				field := pair[:idx]
				value := pair[idx+1:]
				stepMap[field] = value
			}

			recomputeCounters(raw)

			if err := saveWorkflow(wfPath, raw); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation or write error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=update-step stepId=%s lockHeldMs=%d validatedOk=true\n",
				stepID, lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&setPairs, "set", nil, "field=value pair to set on the step (repeatable)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
