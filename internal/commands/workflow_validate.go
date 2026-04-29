package commands

import (
	"fmt"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowValidate(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:          "validate",
		Short:        "Validate the structural integrity of a workflow.json",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			typed, _, err := loadWorkflow(path)
			if err != nil {
				return err
			}

			errs := wf.Validate(typed)
			if len(errs) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "valid")
				return nil
			}

			for _, e := range errs {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", e.Path, e.Message)
			}
			return fmt.Errorf("%d validation error(s)", len(errs))
		},
	}
	parent.AddCommand(cmd)
}
