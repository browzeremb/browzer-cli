package commands

import (
	"encoding/json"
	"fmt"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// legalConfigValues constrains legal values for known config keys.
// A nil value means any string is accepted.
var legalConfigValues = map[string]map[string]bool{
	"mode": {
		"autonomous": true,
		"review":     true,
	},
}

func registerWorkflowSetConfig(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:          "set-config <key> <value>",
		Short:        "Set a config field in workflow.json",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			rawValue := args[1]

			// Validate known keys.
			if legal, ok := legalConfigValues[key]; ok {
				if !legal[rawValue] {
					msg := fmt.Sprintf("illegal value %q for config key %q", rawValue, key)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", msg)
					return fmt.Errorf("%s", msg)
				}
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

			configRaw, ok := raw["config"]
			if !ok {
				configRaw = map[string]any{}
			}
			configMap, ok := configRaw.(map[string]any)
			if !ok {
				configMap = map[string]any{}
			}

			// Auto-detect type: try JSON parse first, fall back to string.
			var parsed any
			if jsonErr := json.Unmarshal([]byte(rawValue), &parsed); jsonErr != nil {
				parsed = rawValue
			}
			configMap[key] = parsed
			configMap["setAt"] = time.Now().UTC().Format(time.RFC3339)

			raw["config"] = configMap
			raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)

			if err := saveWorkflow(wfPath, raw); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "validation or write error: %v\n", err)
				return err
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"verb=set-config key=%s lockHeldMs=%d validatedOk=true\n",
				key, lockHeld.Milliseconds())
			return nil
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
