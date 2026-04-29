package commands

import (
	"encoding/json"
	"fmt"
	"os"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// workflowCmd is the parent cobra command for all `browzer workflow` subcommands.
var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Inspect and validate workflow.json files",
	Long:  "Read and validate Browzer feature workflow.json files.\n\nRun `browzer workflow [command] --help` for subcommand details.",
}

// registerWorkflow adds the workflow command group to parent.
func registerWorkflow(parent *cobra.Command) {
	// Clone the command so each test gets its own instance (persistent flags
	// must not leak between test runs that share the package-level variable).
	cmd := &cobra.Command{
		Use:   workflowCmd.Use,
		Short: workflowCmd.Short,
		Long:  workflowCmd.Long,
	}
	cmd.PersistentFlags().String("workflow", "", "path to workflow.json (overrides BROWZER_WORKFLOW env and walk-up discovery)")
	cmd.PersistentFlags().Bool("no-lock", false, "skip advisory file lock (use for read-only commands)")

	registerWorkflowAppendReviewHistory(cmd)
	registerWorkflowAppendStep(cmd)
	registerWorkflowCompleteStep(cmd)
	registerWorkflowGetConfig(cmd)
	registerWorkflowGetStep(cmd)
	registerWorkflowPatch(cmd)
	registerWorkflowQuery(cmd)
	registerWorkflowSchema(cmd)
	registerWorkflowSetConfig(cmd)
	registerWorkflowSetCurrentStep(cmd)
	registerWorkflowSetStatus(cmd)
	registerWorkflowUpdateStep(cmd)
	registerWorkflowValidate(cmd)

	parent.AddCommand(cmd)
}

// getWorkflowPath resolves the workflow.json path for the given command using
// the --workflow flag, BROWZER_WORKFLOW env, or git-style walk-up.
func getWorkflowPath(cmd *cobra.Command) (string, error) {
	flagPath, _ := cmd.Flags().GetString("workflow")
	if flagPath == "" {
		// Walk up through persistent flags too.
		flagPath, _ = cmd.InheritedFlags().GetString("workflow")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return wf.ResolveWorkflowPath(flagPath, cwd, cmd.Root().ErrOrStderr())
}

// loadWorkflow loads and JSON-decodes the workflow.json found at path.
func loadWorkflow(path string) (wf.Workflow, map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return wf.Workflow{}, nil, fmt.Errorf("read workflow: %w", err)
	}
	var typed wf.Workflow
	if err := json.Unmarshal(data, &typed); err != nil {
		return wf.Workflow{}, nil, fmt.Errorf("parse workflow: %w", err)
	}
	// Also decode to map for field queries.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return typed, nil, fmt.Errorf("parse workflow map: %w", err)
	}
	return typed, raw, nil
}
