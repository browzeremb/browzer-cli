package commands

import (
	"encoding/json"
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

			// Apply the jq expression.
			result, err := wf.ApplyJQ(raw, jqExpr)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "jq error: %v\n", err)
				return err
			}

			// The result must be a map[string]any for a valid workflow document.
			resultMap, ok := result.(map[string]any)
			if !ok {
				// gojq may return map[interface{}]interface{} — round-trip through JSON.
				b, marshalErr := json.Marshal(result)
				if marshalErr != nil {
					return fmt.Errorf("jq result is not a JSON object: %T", result)
				}
				if err := json.Unmarshal(b, &resultMap); err != nil {
					return fmt.Errorf("jq result is not a JSON object: %T", result)
				}
			}

			resultMap["updatedAt"] = time.Now().UTC().Format(time.RFC3339)

			if err := saveWorkflow(wfPath, resultMap); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=patch lockHeldMs=%d validatedOk=true\n",
				lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().StringVar(&jqExpr, "jq", "", "jq expression to apply as mutation (required)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
