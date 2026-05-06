package commands

import (
	"fmt"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// topLevelStateKeys enumerates scalar top-level fields in workflow.json that
// `get-config` exposes for read. Non-scalar top-level fields (operator, notes,
// globalWarnings, steps, config) are deliberately excluded — use `get-step`
// or `--field` for those. Mirrors the CUE SSOT in
// `packages/cli/schemas/workflow-v1.cue` §"#WorkflowV1" top-level scalar fields.
//
// The skill ecosystem (`packages/skills/skills/orchestrate-task-delivery/SKILL.md`
// Step 4, `packages/cli/CLAUDE.md` §".browzer/active-step hybrid-cache",
// `packages/skills/skills/orchestrate-task-delivery/references/pipeline-phases.md`
// "Read verbs" table) all document `get-config currentStepId` as a valid read.
// Without this fall-through the documented pattern emits "field not found"
// because `currentStepId` lives at top level, not under `.config`.
var topLevelStateKeys = map[string]bool{
	"schemaVersion":   true,
	"pluginVersion":   true,
	"featureId":       true,
	"featureName":     true,
	"featDir":         true,
	"originalRequest": true,
	"startedAt":       true,
	"updatedAt":       true,
	"completedAt":     true,
	"totalElapsedMin": true,
	"currentStepId":   true,
	"nextStepId":      true,
	"totalSteps":      true,
	"completedSteps":  true,
}

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

			// Top-level state keys: read directly from the workflow root.
			// `set-current-step` and the workflow seeder write these outside
			// the `.config` section.
			if topLevelStateKeys[key] {
				val, err := wf.GetField(raw, key, false)
				if err != nil {
					return fmt.Errorf("config key %q: %w", key, err)
				}
				return emitReadJSON(cmd, val)
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
			return emitReadJSON(cmd, val)
		},
	}
	cmd.Flags().String("save", "", "write the read payload to <path> instead of stdout")
	parent.AddCommand(cmd)
}
