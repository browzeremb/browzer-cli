package commands

import (
	"fmt"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowGetConfig(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:          "get-config <key>",
		Short:        "Print a config field from workflow.json",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			path, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			_, raw, err := loadWorkflow(path)
			if err != nil {
				return err
			}

			// Extract .config.<key> from the raw map.
			configRaw, ok := raw["config"]
			if !ok {
				return fmt.Errorf("config key %q not found: workflow has no config section", key)
			}

			val, err := wf.GetField(configRaw, key, false)
			if err != nil {
				return fmt.Errorf("config key %q: %w", key, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), val)
			return nil
		},
	}
	parent.AddCommand(cmd)
}
