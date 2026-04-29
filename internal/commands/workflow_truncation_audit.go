package commands

import (
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowTruncationAudit(parent *cobra.Command) {
	var payloadFile string
	var lastCheckpoint string
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "truncation-audit <stepId>",
		Short: "Append a truncation-suspected warning entry to a step's warnings[]",
		Long: `Reads a JSON payload (from --payload file or stdin) with shape:
  { "filesModified": [...], "filesCreated": [...], "filesDeleted": [...] }

and appends a warning entry to the step's warnings[] array:
  { at, kind: "truncation-suspected", filesModified, filesCreated, filesDeleted,
    lastCheckpoint, remediation: "re-dispatch with subagent-preamble §4.5 emphasis" }

--last-checkpoint can also be supplied via the payload's "lastCheckpoint" field.
Closes Phase 3 item #11.`,
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
			payloadBytes, err := readPayload(cmd, payloadFile)
			if err != nil {
				return fmt.Errorf("read payload: %w", err)
			}
			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}
			mutArgs := wf.MutatorArgs{
				Args:    []string{stepID, lastCheckpoint},
				Payload: payloadBytes,
			}
			return dispatchToDaemonOrFallback(cmd, wfPath, "truncation-audit", mutArgs, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().StringVar(&payloadFile, "payload", "", "path to JSON payload file (reads from stdin if omitted)")
	cmd.Flags().StringVar(&lastCheckpoint, "last-checkpoint", "", "human-readable description of the last known good checkpoint")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
