package commands

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/schema"
	"github.com/spf13/cobra"
)

// registerWorkflowDescribeStepType wires `browzer workflow describe-step-type
// <STEP_NAME>` under the workflow command group.
//
// Usage:
//
//	browzer workflow describe-step-type <STEP_NAME> [--field <jqpath>] [--required-only] [--json]
//
// STEP_NAME must be one of the canonical step type names or the special alias
// "workflow" (for the top-level #WorkflowV1 schema). Use --json for a
// machine-readable JSON array; --field for a narrow jq sub-projection;
// --required-only to filter to fields with no CUE default.
//
// The command honors --quiet / BROWZER_WORKFLOW_QUIET=1 / --llm / BROWZER_LLM=1
// to suppress the post-output audit telemetry line on success (errors still
// print to stderr). Under the LLM gate the audit line is routed to the SQLite
// tracker rather than stderr so agent tool-result contexts stay clean.
func registerWorkflowDescribeStepType(parent *cobra.Command) {
	var fieldPath string
	var requiredOnly bool
	var jsonMode bool

	allowlist := make([]string, len(schema.ValidStepNames))
	copy(allowlist, schema.ValidStepNames)
	sort.Strings(allowlist)

	cmd := &cobra.Command{
		Use:   "describe-step-type <STEP_NAME>",
		Short: "Describe a workflow step type's schema fields",
		Long: fmt.Sprintf(`Print a description of the fields defined for a given workflow step type.

STEP_NAME must be one of:
  %s

The special alias "workflow" describes the top-level #WorkflowV1 fields.

Output modes:
  (default)      Markdown table: Field | Required | Type | AddedIn | Description
  --json         JSON array of field objects (sorted by path, byte-identical)
  --field <path> jq-style path into the field projection (returns JSON)
  --required-only  filter to fields with no CUE default (must be supplied)

Flags --field and --required-only can be combined.
--json and --field are mutually exclusive (--field implies JSON output).

Examples:
  browzer workflow describe-step-type TASK
  browzer workflow describe-step-type TASK --json
  browzer workflow describe-step-type TASK --field task.execution.scopeAdjustments
  browzer workflow describe-step-type CODE_REVIEW --field codeReview.regressionRun --json
  browzer workflow describe-step-type TASK --required-only`,
			strings.Join(allowlist, "\n  "),
		),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepName := args[0]

			// Validate before calling into the schema package so the error
			// message is consistent and cobra returns non-zero.
			if !isKnownStepType(stepName) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"unknown step type %q\nallowed: %s\n",
					stepName,
					strings.Join(allowlist, ", "),
				)
				return fmt.Errorf("unknown step type %q", stepName)
			}

			opts := schema.DescribeOpts{
				Field:        fieldPath,
				RequiredOnly: requiredOnly,
				JSON:         jsonMode,
			}

			result, err := schema.DescribeStepType(stepName, opts)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "describe-step-type: %v\n", err)
				return err
			}

			// Write the output — never silenced regardless of --quiet / --llm.
			_, _ = fmt.Fprint(cmd.OutOrStdout(), result)
			// Ensure the output ends with a newline when it doesn't already
			// (e.g. --json returns a compact array without a trailing newline).
			if len(result) > 0 && result[len(result)-1] != '\n' {
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fieldPath, "field", "", "jq-style path to filter the field projection (implies JSON output for the matched value)")
	cmd.Flags().BoolVar(&requiredOnly, "required-only", false, "show only fields with no CUE default (required fields)")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit a JSON array of field objects instead of a Markdown table")

	parent.AddCommand(cmd)
}

// isKnownStepType reports whether name appears in schema.ValidStepNames.
func isKnownStepType(name string) bool {
	return slices.Contains(schema.ValidStepNames, name)
}
