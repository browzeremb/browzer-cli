package commands

import (
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// legalTransitions defines which from→to status transitions are permitted.
// Terminal statuses (COMPLETED, SKIPPED, STOPPED) have no outgoing transitions.
var legalTransitions = map[wf.StepStatus]map[wf.StepStatus]bool{
	wf.StatusPending: {
		wf.StatusRunning:        true,
		wf.StatusAwaitingReview: true,
		wf.StatusCompleted:      true,
		wf.StatusSkipped:        true,
		wf.StatusStopped:        true,
	},
	wf.StatusRunning: {
		wf.StatusCompleted:      true,
		wf.StatusAwaitingReview: true,
		wf.StatusStopped:        true,
	},
	wf.StatusAwaitingReview: {
		wf.StatusCompleted: true,
		wf.StatusSkipped:   true,
		wf.StatusStopped:   true,
	},
	wf.StatusPausedPendingOp: {
		wf.StatusRunning:        true,
		wf.StatusCompleted:      true,
		wf.StatusSkipped:        true,
		wf.StatusStopped:        true,
		wf.StatusAwaitingReview: true,
	},
	// Terminal states: no outgoing transitions.
	wf.StatusCompleted: {},
	wf.StatusSkipped:   {},
	wf.StatusStopped:   {},
}

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

			currentStatus := fmt.Sprintf("%v", stepMap["status"])

			// Validate transition.
			allowed, fromKnown := legalTransitions[currentStatus]
			if !fromKnown {
				// Unknown current status: allow transition to any legal status.
				allowed = legalTransitions[wf.StatusPending]
			}
			if !allowed[newStatus] {
				msg := fmt.Sprintf("illegal status transition %s → %s", currentStatus, newStatus)
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", msg)
				return fmt.Errorf("%s", msg)
			}

			stepMap["status"] = newStatus
			if newStatus == wf.StatusCompleted {
				now := time.Now().UTC().Format(time.RFC3339)
				stepMap["completedAt"] = now
			}

			recomputeCounters(raw)

			if err := saveWorkflow(wfPath, raw); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation or write error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=set-status stepId=%s status=%s lockHeldMs=%d validatedOk=true\n",
				stepID, newStatus, lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
