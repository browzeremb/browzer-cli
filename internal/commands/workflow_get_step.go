package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	wfview "github.com/browzeremb/browzer-cli/internal/workflow/view"
	"github.com/spf13/cobra"
)

// registerWorkflowGetStep wires `workflow get-step <ID>` (subcommand under
// `workflow`). The top-level `browzer get-step <ID>` alias is registered in
// root.go via registerGetStep.
func registerWorkflowGetStep(parent *cobra.Command) {
	parent.AddCommand(newGetStepCommand(false))
}

// registerGetStep adds the top-level `browzer get-step <PHASE>` alias. Differs
// from the workflow-subcommand variant only in that it takes `--id <feat>`
// instead of relying on the workflow-group `--workflow` flag.
func registerGetStep(root *cobra.Command) {
	root.AddCommand(newGetStepCommand(true))
}

func newGetStepCommand(topLevel bool) *cobra.Command {
	var jsonMode bool
	var idFlag string

	cmd := &cobra.Command{
		Use:   "get-step <PHASE>",
		Short: "Print a workflow step formatted for LLM context",
		Long: `Print a workflow step formatted for LLM context.

Default: rich markdown that consolidates the step body, linked PRD slice,
deps, skills, invariants, and done-when. Use --json for a stable struct
documented as #StepView in the CUE schema.

PHASE accepts: PRD, TASKS, TASK_NN (zero-padded), CODE_REVIEW,
RECEIVING_CODE_REVIEW, WRITE_TESTS, UPDATE_DOCS, FEATURE_ACCEPTANCE,
COMMIT, BRAINSTORM, ORIGINAL_REQUEST, CONFIG.

ORIGINAL_REQUEST is a virtual phase: it returns the verbatim ask
recorded by 'workflow init' (no real step needs to exist). Use it as
a fallback context when an earlier phase like BRAINSTORM was skipped.

CONFIG is a virtual phase: it returns the workflow's top-level config
block (mode, setAt, executionStrategy). Useful for skills that need to
honor the operator-resolved executionStrategy without a separate read.

Exit codes: 0 success, 2 step not found / phase unknown.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			phase := args[0]

			path, err := resolveWorkflowPathForGetSave(cmd, idFlag, topLevel)
			if err != nil {
				return err
			}

			_, raw, err := loadWorkflow(path)
			if err != nil {
				return err
			}

			view, err := wfview.Build(raw, phase)
			if err != nil {
				// Self-heal: if no step exists yet but a staging file is
				// present (autosave hook never fired — e.g. operator wrote
				// via Bash heredoc, or in another session), persist it on
				// the fly and re-build the view. Skips virtual phases and
				// TASK_NN (TASK steps must be seeded by tasks-manifest).
				if recovered, healed := tryHealFromStaging(cmd, path, phase); healed {
					_, raw2, err2 := loadWorkflow(path)
					if err2 == nil {
						view, err = wfview.Build(raw2, phase)
					} else {
						err = err2
					}
					_ = recovered
				}
				if err != nil {
					return cliExitErr(2, err)
				}
			}

			if jsonMode {
				b, err := json.MarshalIndent(view, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal step view: %w", err)
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return err
			}

			md, err := wfview.RenderMarkdown(view)
			if err != nil {
				return fmt.Errorf("render markdown: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), md)
			return err
		},
	}

	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit a stable JSON #StepView struct instead of markdown")
	cmd.Flags().StringVar(&idFlag, "id", "", "feature id; resolves docs/browzer/<id>/workflow.json (alternative to --workflow)")
	if topLevel {
		_ = cmd.MarkFlagRequired("id")
	}
	return cmd
}

// tryHealFromStaging looks for a staged artefact matching the requested phase
// next to workflow.json (`<featDir>/staging/<PHASE>.{md,json}`) and runs
// save-step against it. Returns (path, true) on success, ("", false) when no
// staging file exists or the phase is not eligible for self-heal.
//
// Eligible phases are the persistable PHASE names accepted by save-step;
// virtual phases (CONFIG, ORIGINAL_REQUEST) and TASK_NN are excluded —
// those have no staging file mapping or are seeded by upstream verbs.
func tryHealFromStaging(cmd *cobra.Command, workflowPath, phase string) (string, bool) {
	switch phase {
	case "ORIGINAL_REQUEST", "CONFIG":
		return "", false
	}
	if strings.HasPrefix(phase, "TASK_") && phase != "TASKS" {
		return "", false
	}
	stagingDir := filepath.Join(filepath.Dir(workflowPath), "staging")
	for _, ext := range []string{"md", "json"} {
		candidate := filepath.Join(stagingDir, phase+"."+ext)
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		srcFormat := "json"
		if ext == "md" {
			srcFormat = "md"
		}
		parsed, err := parsePayload(data, srcFormat, phase)
		if err != nil {
			continue
		}
		args := wf.MutatorArgs{Args: []string{phase}, Payload: mustJSON(parsed)}
		// Block until durable — the immediate next read in this same call
		// must see the freshly-persisted step.
		if _, err := wf.ApplyAndPersist(workflowPath, "save-step", args, true); err != nil {
			continue
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[get-step] self-heal: persisted %s from %s\n", phase, candidate)
		return candidate, true
	}
	return "", false
}
