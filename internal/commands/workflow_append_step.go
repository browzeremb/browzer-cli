package commands

import (
	"encoding/json"
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

			// Read payload.
			payloadBytes, err := readPayload(cmd, payloadFile)
			if err != nil {
				return fmt.Errorf("read payload: %w", err)
			}

			// Parse the payload as a step map.
			var stepMap map[string]any
			if err := json.Unmarshal(payloadBytes, &stepMap); err != nil {
				return fmt.Errorf("parse step payload: %w", err)
			}

			// Load the current workflow (inside the lock window).
			_, raw, err := loadWorkflow(wfPath)
			if err != nil {
				return err
			}

			// Append the step.
			stepsRaw := raw["steps"]
			stepsSlice, _ := stepsRaw.([]any)
			stepsSlice = append(stepsSlice, stepMap)
			raw["steps"] = stepsSlice

			recomputeCounters(raw)

			stepID, _ := stepMap["stepId"].(string)

			if err := saveWorkflow(wfPath, raw); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation or write error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=append-step stepId=%s lockHeldMs=%d validatedOk=true\n",
				stepID, lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().StringVar(&payloadFile, "payload", "", "path to step JSON payload file (reads from stdin if omitted)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
