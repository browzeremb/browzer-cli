package commands

import (
	"fmt"
	"time"

	"github.com/browzeremb/browzer-cli/internal/schema"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowAppendStep(parent *cobra.Command) {
	var payloadFile string
	var scaffoldStepType string
	var lockTimeout time.Duration
	var idFlag string

	cmd := &cobra.Command{
		Use:   "append-step",
		Short: "Append a step to workflow.json",
		Long: `Append a step to workflow.json.

Two modes:

  --payload <file>      Append the step described by the JSON payload at
                        <file> (or stdin when omitted). Acquires the advisory
                        lock, validates the post-mutation document against
                        the CUE SSOT, and writes via tmp + rename.

  --scaffold <STEP>     Preview-only. Emit a JSON skeleton for the named
                        step type containing EXACTLY the required fields,
                        each populated with a type-correct empty value
                        (""/[]/{}/false/0). Does NOT touch workflow.json.
                        Use to bootstrap a payload without hand-rolling the
                        wrapper (StepBase) shape, then patch in real values
                        before piping back through --payload. Pairs with
                        --save <path> to write the skeleton to disk.

The two modes are mutually exclusive; passing both fails fast.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if scaffoldStepType != "" {
				if payloadFile != "" {
					return fmt.Errorf("--scaffold and --payload are mutually exclusive")
				}
				skeleton, err := schema.ScaffoldStep(scaffoldStepType)
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "append-step --scaffold: %v\n", err)
					return err
				}
				return emitReadOutput(cmd, skeleton)
			}

			wfPath, err := resolveWorkflowPathForGetSave(cmd, idFlag, false)
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
	cmd.Flags().StringVar(&scaffoldStepType, "scaffold", "", "preview-only: emit a JSON skeleton for the named step type (mutually exclusive with --payload)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	cmd.Flags().String("save", "", "write the scaffold output to <path> instead of stdout (only meaningful with --scaffold)")
	cmd.Flags().StringVar(&idFlag, "id", "", "feature id; resolves docs/browzer/<id>/workflow.json (alternative to --workflow)")
	parent.AddCommand(cmd)
}
