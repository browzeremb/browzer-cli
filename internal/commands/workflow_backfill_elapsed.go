package commands

import (
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// registerWorkflowBackfillElapsed wires `browzer workflow backfill-elapsed`,
// the recovery verb that walks every step in workflow.json and stamps
// elapsedMin = (completedAt - startedAt) / 60 (minutes) on every step where
// both timestamps are set AND the current elapsedMin is null/zero. Also
// refreshes the workflow-level totalElapsedMin by summing per-step values.
//
// Idempotent by design — running twice on the same workflow is a no-op the
// second time. Recovery story (RETRO §2): the orchestrator's Step 7 closure
// was the only place that stamped elapsedMin; if the orchestrator was
// interrupted before closure ran, completed steps ended up with
// elapsedMin: null. This verb fixes that without re-running the orchestrator.
func registerWorkflowBackfillElapsed(parent *cobra.Command) {
	var lockTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "backfill-elapsed",
		Short: "Backfill elapsedMin on every step where both timestamps are set",
		Long: "Walk every step in workflow.json and stamp elapsedMin =\n" +
			"(completedAt - startedAt) / 60 (minutes) when:\n" +
			"  - completedAt is set (non-empty),\n" +
			"  - startedAt is set (non-empty),\n" +
			"  - elapsedMin is currently null / missing / zero.\n" +
			"\n" +
			"Steps with a positive elapsedMin are left untouched. Workflow-level\n" +
			"totalElapsedMin is recomputed from the resulting per-step values; if\n" +
			"no step contributes a value, falls back to (MAX(per-step completedAt)\n" +
			"- startedAt) at the workflow root (see F-5 below).\n" +
			"\n" +
			"Idempotent: safe to run any number of times. Mid-flow execution is\n" +
			"supported — only completed steps are affected.\n" +
			"\n" +
			"Cost: O(steps) — the verb walks every step while holding the\n" +
			"workflow advisory lock. Today's workflows are O(20) steps so the\n" +
			"lock window is sub-millisecond, but invoking on a workflow with\n" +
			"hundreds of steps blocks sibling mutators queued behind it.\n" +
			"\n" +
			"Fallback when no per-step elapsedMin: derives totalElapsedMin from\n" +
			"startedAt -> MAX(per-step completedAt). The previous fallback used\n" +
			"workflow-root updatedAt, which conflated 'time the verb ran' with\n" +
			"'wall-clock spent on the feature' on idle workflows (F-5).\n" +
			"\n" +
			"Standard mutator flag surface: --workflow / --async / --await /\n" +
			"--sync / --quiet / --no-lock / --lock-timeout.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}
			noLock, _ := cmd.Flags().GetBool("no-lock")
			if !noLock {
				noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
			}
			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}
			return dispatchToDaemonOrFallback(cmd, wfPath, "backfill-elapsed", wf.MutatorArgs{}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
}
