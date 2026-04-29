package commands

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func registerWorkflowSchema(parent *cobra.Command) {
	var jsonSchemaFlag bool
	var fieldPath string

	cmd := &cobra.Command{
		Use:          "schema",
		Short:        "Print the JSON Schema (Draft 2020-12) describing workflow.json v1",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			schema := buildWorkflowSchema()

			if fieldPath != "" {
				sub, err := scopeSchema(schema, fieldPath)
				if err != nil {
					return err
				}
				schema = sub
			}

			if jsonSchemaFlag {
				b, err := json.MarshalIndent(schema, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal schema: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), buildSchemaMarkdown())
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonSchemaFlag, "json-schema", false, "emit Draft 2020-12 JSON Schema (machine-readable JSON)")
	cmd.Flags().StringVar(&fieldPath, "field", "", "scope to a sub-path (e.g. steps.items, $defs.Step)")

	parent.AddCommand(cmd)
}

// buildWorkflowSchema returns a map[string]any representing the Draft 2020-12
// JSON Schema for workflow.json v1. The schema is hand-authored for maximum
// readability and correctness; no reflection-based generation is used.
func buildWorkflowSchema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://browzer.dev/schemas/workflow-v1.json",
		"title":   "Browzer Workflow v1",
		"type":    "object",
		"required": []any{
			"schemaVersion",
			"featureId",
			"startedAt",
			"steps",
		},
		"properties": map[string]any{
			"schemaVersion": map[string]any{
				"const":       1,
				"description": "Schema version — always 1 for v1 workflow files.",
			},
			"featureId": map[string]any{
				"type":        "string",
				"description": "Unique identifier for the feature (usually a UUID or slug).",
			},
			"featureName": map[string]any{
				"type":        "string",
				"description": "Human-readable feature name.",
			},
			"featDir": map[string]any{
				"type":        "string",
				"description": "Relative path to the feature directory (e.g. docs/browzer/feat-xxx).",
			},
			"originalRequest": map[string]any{
				"type":        "string",
				"description": "The original operator request that initiated this workflow.",
			},
			"operator": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"locale": map[string]any{"type": "string"},
				},
				"description": "Operator metadata (locale, etc.).",
			},
			"config": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode": map[string]any{
						"enum":        []any{"autonomous", "review", nil},
						"description": "Operator-chosen execution mode.",
					},
					"setAt": map[string]any{
						"type": []any{"string", "null"},
					},
				},
				"description": "Operator-chosen configuration for this workflow run.",
			},
			"startedAt": map[string]any{
				"type":        "string",
				"format":      "date-time",
				"description": "ISO-8601 timestamp when the workflow was created.",
			},
			"updatedAt": map[string]any{
				"type":        "string",
				"format":      "date-time",
				"description": "ISO-8601 timestamp of the last mutation.",
			},
			"totalElapsedMin": map[string]any{
				"type":        "number",
				"description": "Cumulative elapsed minutes across all steps.",
			},
			"currentStepId": map[string]any{
				"type":        []any{"string", "null"},
				"description": "stepId of the currently active step, or null.",
			},
			"nextStepId": map[string]any{
				"type":        []any{"string", "null"},
				"description": "stepId of the next step to run, or null.",
			},
			"totalSteps": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Total number of steps in the workflow.",
			},
			"completedSteps": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Number of steps with status COMPLETED or SKIPPED.",
			},
			"notes": map[string]any{
				"type":        "array",
				"description": "Free-form notes attached to the workflow.",
			},
			"globalWarnings": map[string]any{
				"type":        "array",
				"description": "Warnings that apply to the entire workflow (not a specific step).",
			},
			"steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"$ref": "#/$defs/Step",
				},
				"description": "Ordered list of workflow steps.",
			},
		},
		"$defs": map[string]any{
			"Step": map[string]any{
				"type": "object",
				"required": []any{
					"stepId",
					"name",
					"status",
					"applicability",
				},
				"properties": map[string]any{
					"stepId": map[string]any{
						"type":        "string",
						"description": "Unique identifier for this step instance.",
					},
					"name": map[string]any{
						"enum": []any{
							"BRAINSTORMING",
							"PRD",
							"TASKS_MANIFEST",
							"TASK",
							"CODE_REVIEW",
							"RECEIVING_CODE_REVIEW",
							"WRITE_TESTS",
							"UPDATE_DOCS",
							"FEATURE_ACCEPTANCE",
							"COMMIT",
							"FIX_FINDINGS",
						},
						"description": "Step type — determines the shape of the task payload.",
					},
					"taskId": map[string]any{
						"type":        "string",
						"description": "Reference to a task in the TASKS_MANIFEST (TASK steps only).",
					},
					"status": map[string]any{
						"enum": []any{
							"PENDING",
							"RUNNING",
							"AWAITING_REVIEW",
							"COMPLETED",
							"SKIPPED",
							"STOPPED",
							"PAUSED_PENDING_OPERATOR",
						},
						"description": "Current lifecycle status of this step.",
					},
					"applicability": map[string]any{
						"$ref":        "#/$defs/Applicability",
						"description": "Whether this step is applicable for this feature.",
					},
					"startedAt": map[string]any{
						"type":   []any{"string", "null"},
						"format": "date-time",
					},
					"completedAt": map[string]any{
						"type":   []any{"string", "null"},
						"format": "date-time",
					},
					"elapsedMin": map[string]any{
						"type": "number",
					},
					"retryCount": map[string]any{
						"type":    "integer",
						"minimum": 0,
					},
					"itDependsOn": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"nextStep": map[string]any{
						"type": []any{"string", "null"},
					},
					"skillsToInvoke": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"skillsInvoked": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"owner": map[string]any{
						"type": []any{"string", "null"},
					},
					"worktrees": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"used": map[string]any{"type": "boolean"},
							"worktrees": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"name":   map[string]any{"type": "string"},
										"status": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
					"warnings": map[string]any{
						"type": "array",
					},
					"reviewHistory": map[string]any{
						"type":        "array",
						"description": "Review exchanges recorded when config.mode == review.",
					},
					"task": map[string]any{
						"type":        "object",
						"description": "Step-type-specific payload. Shape varies by name; see packages/skills/references/workflow-schema.md for per-type semantics.",
					},
				},
			},
			"Applicability": map[string]any{
				"type": "object",
				"required": []any{
					"applicable",
				},
				"properties": map[string]any{
					"applicable": map[string]any{
						"type": "boolean",
					},
					"reason": map[string]any{
						"type": "string",
					},
				},
			},
		},
	}
}

// scopeSchema traverses the schema map following a dot-separated path and
// returns the sub-schema at that path. Supported path segments are map keys.
// When a key is not found directly in the current object, scopeSchema
// automatically checks the "properties" sub-object (JSON Schema convention),
// enabling convenience paths like "steps.items" instead of
// "properties.steps.items".
//
// Example paths:
//   - "steps"         → the steps property schema (via properties.steps)
//   - "steps.items"   → the Step $ref object (via properties.steps.items)
//   - "$defs.Step"    → the Step $defs entry (direct key)
func scopeSchema(schema map[string]any, path string) (map[string]any, error) {
	parts := strings.Split(path, ".")
	current := any(schema)

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field path %q not found: segment %q cannot be traversed (not an object)", path, part)
		}
		// Direct key lookup first.
		val, ok := m[part]
		if !ok {
			// Fallback: check inside "properties" (JSON Schema convention).
			if props, pok := m["properties"].(map[string]any); pok {
				val, ok = props[part]
			}
		}
		if !ok {
			return nil, fmt.Errorf("field path %q not found: segment %q does not exist", path, part)
		}
		current = val
	}

	result, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("field path %q does not resolve to an object schema", path)
	}
	return result, nil
}

// buildSchemaMarkdown returns a human-readable markdown summary of the
// workflow.json v1 schema for quick reference.
func buildSchemaMarkdown() string {
	var b strings.Builder
	b.WriteString("# Browzer workflow.json — Schema v1\n\n")

	b.WriteString("## Top-level fields\n")
	b.WriteString("- schemaVersion (1, required)\n")
	b.WriteString("- featureId (string, required)\n")
	b.WriteString("- featureName (string)\n")
	b.WriteString("- featDir (string)\n")
	b.WriteString("- originalRequest (string)\n")
	b.WriteString("- operator (object: locale)\n")
	b.WriteString("- config (object: mode, setAt)\n")
	b.WriteString("- startedAt (string/date-time, required)\n")
	b.WriteString("- updatedAt (string/date-time)\n")
	b.WriteString("- totalElapsedMin (number)\n")
	b.WriteString("- currentStepId (string|null)\n")
	b.WriteString("- nextStepId (string|null)\n")
	b.WriteString("- totalSteps (integer ≥ 0)\n")
	b.WriteString("- completedSteps (integer ≥ 0)\n")
	b.WriteString("- notes (array)\n")
	b.WriteString("- globalWarnings (array)\n")
	b.WriteString("- steps (array of Step, required)\n\n")

	b.WriteString("## Step types (.steps[].name)\n")
	b.WriteString("- BRAINSTORMING — payload: brainstorm\n")
	b.WriteString("- PRD — payload: prd\n")
	b.WriteString("- TASKS_MANIFEST — payload: tasksManifest\n")
	b.WriteString("- TASK — payload: task\n")
	b.WriteString("- CODE_REVIEW — payload: codeReview\n")
	b.WriteString("- RECEIVING_CODE_REVIEW — payload: receivingCodeReview\n")
	b.WriteString("- WRITE_TESTS — payload: writeTests\n")
	b.WriteString("- UPDATE_DOCS — payload: updateDocs\n")
	b.WriteString("- FEATURE_ACCEPTANCE — payload: featureAcceptance\n")
	b.WriteString("- COMMIT — payload: commit\n")
	b.WriteString("- FIX_FINDINGS — payload: fixFindings (deprecated — replaced by RECEIVING_CODE_REVIEW; still recognised for legacy workflow.json files)\n\n")

	b.WriteString("## Step lifecycle (.steps[].status)\n")
	b.WriteString("PENDING → RUNNING → COMPLETED | AWAITING_REVIEW | SKIPPED | STOPPED | PAUSED_PENDING_OPERATOR\n\n")

	b.WriteString("For the formal machine-readable schema, run with --json-schema.\n")
	b.WriteString("For full payload semantics (review gate, lock semantics, citation policy), see packages/skills/references/workflow-schema.md.\n")

	return b.String()
}
