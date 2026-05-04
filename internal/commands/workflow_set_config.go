package commands

import (
	"errors"
	"fmt"
	"os"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowSetConfig(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "set-config <key> <value>",
		Short:        "Set a config field in workflow.json (auto-bootstraps an empty schema v2 file when path does not exist)",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			rawValue := args[1]
			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			// Auto-bootstrap on missing file. set-config is the canonical
			// first-touch verb for orchestrate-task-delivery's Step 0 (mode
			// resolution) — prior to this, operators had to Write the
			// skeleton manually because no other verb tolerates a missing
			// path. With auto-bootstrap, the orchestrator's first invocation
			// becomes the implicit init.
			//
			// `browzer workflow init` remains the explicit form for cases
			// that want to populate featureName / originalRequest during
			// initialization; set-config's bootstrap leaves those empty.
			if _, statErr := os.Stat(wfPath); os.IsNotExist(statErr) {
				if bsErr := wf.BootstrapSkeleton(wfPath, wf.BootstrapOptions{}); bsErr != nil &&
					!errors.Is(bsErr, wf.ErrAlreadyExists) {
					return fmt.Errorf("auto-bootstrap before set-config: %w", bsErr)
				}
				_, _ = fmt.Fprintf(
					cmd.ErrOrStderr(),
					"info: auto-bootstrapped workflow.json at %s (no file present)\n",
					wfPath,
				)
			}

			noLock, _ := cmd.Flags().GetBool("no-lock")
			if !noLock {
				noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
			}
			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}
			return dispatchToDaemonOrFallback(cmd, wfPath, "set-config", wf.MutatorArgs{
				Args: []string{key, rawValue},
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
