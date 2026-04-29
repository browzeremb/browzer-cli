package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowGetStep(parent *cobra.Command) {
	var field string
	var jsonMode bool
	var renderTemplate string
	var bashVars bool

	cmd := &cobra.Command{
		Use:          "get-step <stepId>",
		Short:        "Print a step from workflow.json by its stepId",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepID := args[0]

			if renderTemplate != "" && field != "" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "--render and --field are mutually exclusive\n")
				return fmt.Errorf("--render and --field are mutually exclusive")
			}

			if bashVars && (field != "" || jsonMode || renderTemplate != "") {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "--bash-vars is mutually exclusive with --field, --json, and --render\n")
				return fmt.Errorf("--bash-vars is mutually exclusive with --field, --json, and --render")
			}

			path, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			wfObj, raw, err := loadWorkflow(path)
			if err != nil {
				return err
			}

			// Find the step in the raw map.
			stepsRaw, ok := raw["steps"]
			if !ok {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "step %q not found: workflow has no steps\n", stepID)
				return fmt.Errorf("step %q not found", stepID)
			}
			stepsSlice, ok := stepsRaw.([]any)
			if !ok {
				return fmt.Errorf("steps field is not an array")
			}

			var stepMap map[string]any
			for _, s := range stepsSlice {
				sm, ok := s.(map[string]any)
				if !ok {
					continue
				}
				if sm["stepId"] == stepID {
					stepMap = sm
					break
				}
			}
			if stepMap == nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "step %q not found in workflow\n", stepID)
				return fmt.Errorf("step %q not found", stepID)
			}

			if bashVars {
				// Find the typed step from the decoded Workflow.
				var step *wf.Step
				for i := range wfObj.Steps {
					if wfObj.Steps[i].StepID == stepID {
						step = &wfObj.Steps[i]
						break
					}
				}
				if step == nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "step %q not found in workflow\n", stepID)
					return fmt.Errorf("step %q not found", stepID)
				}
				return emitBashVars(*step, cmd.OutOrStdout())
			}

			if renderTemplate != "" {
				// Find the typed step from the decoded Workflow.
				var step *wf.Step
				for i := range wfObj.Steps {
					if wfObj.Steps[i].StepID == stepID {
						step = &wfObj.Steps[i]
						break
					}
				}
				if step == nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "step %q not found in workflow\n", stepID)
					return fmt.Errorf("step %q not found", stepID)
				}
				// Bridge step-type-specific payload fields into step.Task so
				// renderers can unmarshal from a single field. Each step type
				// stores its payload under a different JSON key (e.g. "codeReview",
				// "brainstorm", etc.) rather than "task". If step.Task is empty,
				// look for the canonical payload field in the raw step map.
				if len(step.Task) == 0 {
					payloadKey := stepTypePayloadKey(step.Name)
					if payloadKey != "" {
						if raw, ok := stepMap[payloadKey]; ok {
							if b, merr := json.Marshal(raw); merr == nil {
								step.Task = json.RawMessage(b)
							}
						}
					}
				}
				out, err := wf.Render(*step, renderTemplate)
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "render: %v\n", err)
					return err
				}
				_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
				return nil
			}

			if field != "" {
				val, err := wf.GetField(stepMap, field, jsonMode)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), val)
				return nil
			}

			// Print full step JSON.
			b, err := json.MarshalIndent(stepMap, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal step: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}

	cmd.Flags().StringVar(&field, "field", "", "dot-path expression to extract a specific field from the step")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "output scalars as JSON literals (quoted strings, numbers as JSON)")
	cmd.Flags().StringVar(&renderTemplate, "render", "", "render the step using a named template (e.g. execute-task)")
	cmd.Flags().BoolVar(&bashVars, "bash-vars", false, "output shell-export-compatible KEY=value lines suitable for eval")
	parent.AddCommand(cmd)
}

// stepTypePayloadKey returns the JSON object key that holds the step-type-specific
// payload for a given step name. Returns "" for step types that use "task" or
// have no dedicated payload key.
func stepTypePayloadKey(name string) string {
	switch name {
	case wf.StepBrainstorming:
		return "brainstorm"
	case wf.StepCodeReview:
		return "codeReview"
	case wf.StepUpdateDocs:
		return "updateDocs"
	case wf.StepTasksManifest:
		return "tasksManifest"
	default:
		return ""
	}
}

// bashQuote wraps s in single quotes, escaping embedded single quotes via the
// standard '\'' idiom so the result is safe for eval.
func bashQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", `'\''`)
	return "'" + escaped + "'"
}

// emitBashVars writes shell-variable assignments for all common scalar fields
// of step and, depending on step.Name, the step-type-specific scalar fields.
// Each line is KEY=VALUE where string values are single-quoted. Boolean fields
// use yes/no; counts are bare integers. Null/missing values are skipped.
func emitBashVars(step wf.Step, w io.Writer) error {
	var b strings.Builder

	emit := func(key, val string) {
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(val)
		b.WriteString("\n")
	}
	emitStr := func(key, val string) {
		if val == "" {
			return
		}
		emit(key, bashQuote(val))
	}
	emitBool := func(key string, val bool) {
		if val {
			emit(key, "'true'")
		} else {
			emit(key, "'false'")
		}
	}
	emitYesNo := func(key string, val bool) {
		if val {
			emit(key, "'yes'")
		} else {
			emit(key, "'no'")
		}
	}
	emitInt := func(key string, val int) {
		emit(key, fmt.Sprintf("%d", val))
	}

	// Common scalars.
	emitStr("step_id", step.StepID)
	emitStr("step_name", step.Name)
	emitStr("step_status", step.Status)
	// applicability_applicable is always present (struct field, not pointer)
	emitBool("applicability_applicable", step.Applicability.Applicable)
	emitStr("started_at", step.StartedAt)
	if step.CompletedAt != nil {
		emitStr("completed_at", *step.CompletedAt)
	}
	emitStr("next_step", step.NextStep)

	// Step-type-specific scalars: unmarshal Task payload on demand.
	switch step.Name {
	case wf.StepTask:
		if len(step.Task) == 0 {
			break
		}
		var t map[string]any
		if err := json.Unmarshal(step.Task, &t); err != nil {
			break
		}
		if v, ok := t["title"].(string); ok {
			emitStr("task_title", v)
		}
		if v, ok := t["status"].(string); ok {
			emitStr("task_status", v)
		}
		if v, ok := t["suggestedModel"].(string); ok {
			emitStr("task_suggested_model", v)
		}
		if v, ok := t["trivial"].(bool); ok {
			emitYesNo("task_trivial", v)
		}
		if reviewer, ok := t["reviewer"].(map[string]any); ok {
			if tdd, ok := reviewer["tddDecision"].(map[string]any); ok {
				if v, ok := tdd["applicable"].(bool); ok {
					emitYesNo("task_tdd_applicable", v)
				}
			}
		}
		if deps, ok := t["dependsOn"].([]any); ok {
			emitInt("task_dependson_count", len(deps))
		}
		if inv, ok := t["invariants"].([]any); ok {
			emitInt("task_invariants_count", len(inv))
		}
		if scope, ok := t["scope"].([]any); ok {
			emitInt("task_scope_count", len(scope))
		}

	case wf.StepPRD:
		if len(step.Task) == 0 {
			break
		}
		var t map[string]any
		if err := json.Unmarshal(step.Task, &t); err != nil {
			break
		}
		if v, ok := t["title"].(string); ok {
			emitStr("prd_title", v)
		}
		if frs, ok := t["functionalRequirements"].([]any); ok {
			emitInt("prd_total_frs", len(frs))
		}
		if acs, ok := t["acceptanceCriteria"].([]any); ok {
			emitInt("prd_total_acs", len(acs))
		}
		if nfrs, ok := t["nonFunctionalRequirements"].([]any); ok {
			emitInt("prd_total_nfrs", len(nfrs))
		}

	case wf.StepTasksManifest:
		if len(step.Task) == 0 {
			break
		}
		var t map[string]any
		if err := json.Unmarshal(step.Task, &t); err != nil {
			break
		}
		if tasks, ok := t["tasks"].([]any); ok {
			emitInt("manifest_total_tasks", len(tasks))
		}

	case wf.StepCodeReview:
		if len(step.Task) == 0 {
			break
		}
		var t map[string]any
		if err := json.Unmarshal(step.Task, &t); err != nil {
			break
		}
		if findings, ok := t["findings"].([]any); ok {
			emitInt("code_review_findings_count", len(findings))
			high, med, low := 0, 0, 0
			for _, f := range findings {
				if fm, ok := f.(map[string]any); ok {
					switch fm["severity"] {
					case "high":
						high++
					case "medium":
						med++
					case "low":
						low++
					}
				}
			}
			emitInt("code_review_high_count", high)
			emitInt("code_review_medium_count", med)
			emitInt("code_review_low_count", low)
		}
		if v, ok := t["mode"].(string); ok {
			emitStr("code_review_mode", v)
		}
		if v, ok := t["tier"].(string); ok {
			emitStr("code_review_tier", v)
		}

	case wf.StepBrainstorming:
		if len(step.Task) == 0 {
			break
		}
		var t map[string]any
		if err := json.Unmarshal(step.Task, &t); err != nil {
			break
		}
		if dims, ok := t["dimensions"].(map[string]any); ok {
			if v, ok := dims["primaryUser"].(string); ok {
				emitStr("brainstorm_primary_user", v)
			}
			if v, ok := dims["jobToBeDone"].(string); ok {
				emitStr("brainstorm_job_to_be_done", v)
			}
			if v, ok := dims["successSignal"].(string); ok {
				emitStr("brainstorm_success_signal", v)
			}
		}

	case wf.StepUpdateDocs:
		if len(step.Task) == 0 {
			break
		}
		var t map[string]any
		if err := json.Unmarshal(step.Task, &t); err != nil {
			break
		}
		if patches, ok := t["patches"].([]any); ok {
			emitInt("update_docs_patches_count", len(patches))
		}
		if v, ok := t["budgetUsed"].(float64); ok {
			emitInt("update_docs_budget_used", int(v))
		}
		if v, ok := t["budgetMax"].(float64); ok {
			emitInt("update_docs_budget_max", int(v))
		}

	case wf.StepFeatureAcceptance:
		if len(step.Task) == 0 {
			break
		}
		var t map[string]any
		if err := json.Unmarshal(step.Task, &t); err != nil {
			break
		}
		if v, ok := t["mode"].(string); ok {
			emitStr("fa_mode", v)
		}
		if acs, ok := t["acceptanceCriteria"].([]any); ok {
			emitInt("fa_ac_count", len(acs))
		}
		if nfrs, ok := t["nonFunctionalRequirements"].([]any); ok {
			emitInt("fa_nfr_count", len(nfrs))
		}
		if metrics, ok := t["metrics"].([]any); ok {
			emitInt("fa_metric_count", len(metrics))
		}
	}

	_, err := fmt.Fprint(w, b.String())
	return err
}
