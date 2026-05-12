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
	var inlineEnums bool
	var includeBase bool

	allowlist := make([]string, len(schema.ValidStepNames))
	copy(allowlist, schema.ValidStepNames)
	sort.Strings(allowlist)

	// Build the human-readable step-name list that advertises registered
	// aliases alongside their canonical names. Each alias is appended as
	// "(alias: ALIAS)" immediately after its canonical entry so operators
	// running --help can discover both forms without reading source code.
	//
	// Example output line: "  BRAINSTORMING (alias: BRAINSTORM)"
	//
	// The reverse-map is built at command-registration time (once) from the
	// schema.StepTypeAliases SSOT rather than hard-coded here, so adding a
	// new alias in describe.go is the only change required.
	reverseAliases := make(map[string][]string, len(schema.StepTypeAliases))
	for alias, canonical := range schema.StepTypeAliases {
		reverseAliases[canonical] = append(reverseAliases[canonical], alias)
	}
	// Sort each alias list for deterministic output.
	for canonical := range reverseAliases {
		sort.Strings(reverseAliases[canonical])
	}

	allowlistLines := make([]string, 0, len(allowlist))
	for _, name := range allowlist {
		line := name
		if aliases, ok := reverseAliases[name]; ok {
			line += " (alias: " + strings.Join(aliases, ", ") + ")"
		}
		allowlistLines = append(allowlistLines, line)
	}

	// allowlistPlain is used for the error path so the message stays compact.
	allowlistWithAliases := make([]string, 0, len(allowlist)+len(schema.StepTypeAliases))
	allowlistWithAliases = append(allowlistWithAliases, allowlist...)
	for alias := range schema.StepTypeAliases {
		allowlistWithAliases = append(allowlistWithAliases, alias)
	}
	sort.Strings(allowlistWithAliases)

	cmd := &cobra.Command{
		Use: "describe-step-type <STEP_NAME>",
		// `describe-step` is a legacy alias kept because operators (and
		// LLM agents) typed it during the 2026-05-05 orchestrate-task-
		// delivery session expecting it to work. The cheat-sheet drift
		// audit reads only the `Use:` field, so the alias is invisible
		// to that gate; the canonical name remains describe-step-type.
		Aliases: []string{"describe-step"},
		Short:   "Describe a workflow step type's schema fields",
		Long: fmt.Sprintf(`Print a description of the fields defined for a given workflow step type.

Aliases: describe-step (kept for ergonomics — flags + args are identical).

STEP_NAME must be one of:
  %s

The special alias "workflow" describes the top-level #WorkflowV1 fields.

Output modes:
  (default)       Markdown table: Field | Required | Type | AddedIn | Description
  --json          JSON array of field objects (sorted by path, byte-identical)
  --inline-enums  Compact JSON array of ONLY enumerable fields, each as
                  {path, type, values:[...]} for string disjunctions or
                  {path, type, regex:"..."} for regex constraints.
                  Designed for LLM consumption: lets the agent build a
                  payload that passes CUE validation on first try.
                  Implies --json semantics (machine-readable).
  --field <path>  jq-style path into the field projection (returns JSON)
  --required-only filter to fields with no CUE default (must be supplied)
  --include-base  prepend #StepBase wrapper rows under the @base. path
                  prefix (e.g. @base.stepId, @base.status). Default
                  off for backward compat — opt in when you need the
                  full step shape (wrapper + payload) in one invocation.

Flags --field and --required-only can be combined.
--json and --field are mutually exclusive (--field implies JSON output).
--inline-enums and --field are mutually exclusive.

Examples:
  browzer workflow describe-step-type TASK
  browzer workflow describe-step-type TASK --json
  browzer workflow describe-step-type TASK --include-base --json
  browzer workflow describe-step-type CODE_REVIEW --inline-enums
  browzer workflow describe-step-type TASK --field task.execution.scopeAdjustments
  browzer workflow describe-step-type CODE_REVIEW --field codeReview.regressionRun --json
  browzer workflow describe-step-type TASK --required-only`,
			strings.Join(allowlistLines, "\n  "),
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
					strings.Join(allowlistWithAliases, ", "),
				)
				return fmt.Errorf("unknown step type %q", stepName)
			}

			if inlineEnums && fieldPath != "" {
				return fmt.Errorf("--inline-enums and --field are mutually exclusive")
			}

			opts := schema.DescribeOpts{
				Field:        fieldPath,
				RequiredOnly: requiredOnly,
				JSON:         jsonMode || inlineEnums,
				InlineEnums:  inlineEnums,
				IncludeBase:  includeBase,
			}

			result, err := schema.DescribeStepType(stepName, opts)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "describe-step-type: %v\n", err)
				return err
			}

			// Ensure a trailing newline so file consumers (and stdout
			// readers) can pipe the output without surprise. emitReadOutput
			// routes to --save when set; otherwise stdout. Audit/quiet
			// semantics stay owned by emitReadOutput.
			payload := result
			if len(payload) > 0 && payload[len(payload)-1] != '\n' {
				payload += "\n"
			}
			return emitReadOutput(cmd, []byte(payload))
		},
	}

	cmd.Flags().StringVar(&fieldPath, "field", "", "jq-style path to filter the field projection (implies JSON output for the matched value)")
	cmd.Flags().BoolVar(&requiredOnly, "required-only", false, "show only fields with no CUE default (required fields)")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit a JSON array of field objects instead of a Markdown table")
	cmd.Flags().BoolVar(&inlineEnums, "inline-enums", false, "emit only enumerable fields as {path,type,values}/{path,type,regex} JSON")
	cmd.Flags().BoolVar(&includeBase, "include-base", false, "prepend #StepBase wrapper fields under the @base. path prefix (default false; backward-compat opt-in)")
	cmd.Flags().String("save", "", "write the output to <path> instead of stdout (parity with get-step / get-config / query)")

	parent.AddCommand(cmd)
}

// isKnownStepType reports whether name appears in schema.ValidStepNames or
// is a recognised alias in schema.StepTypeAliases (e.g. "BRAINSTORM",
// "TASKS"). Aliases are resolved to their canonical form before the
// ValidStepNames check so the error path never fires for known aliases.
func isKnownStepType(name string) bool {
	canonical := schema.ResolveStepAlias(name)
	return slices.Contains(schema.ValidStepNames, canonical)
}
