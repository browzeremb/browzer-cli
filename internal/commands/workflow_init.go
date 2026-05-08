package commands

import (
	"errors"
	"fmt"
	"os"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowInit(parent *cobra.Command) {
	var (
		featureID         string
		featureName       string
		originalRequest   string
		operatorLocale    string
		executionStrategy string
		mode              string
		force             bool
	)

	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Seed an empty schema v2 workflow.json at --workflow <path>",
		Long: `Seed an empty schema v2 workflow.json at the path resolved via --workflow,
BROWZER_WORKFLOW, or walk-up. Idempotent by default: exits non-zero with
"already_exists" if the file is present. Pass --force to overwrite.

Use this once per feature, before any other ` + "`browzer workflow`" + ` verb.
Subsequent set-config / append-step / set-status / patch calls operate on the
seeded file and run through the daemon FIFO + standalone fallback as usual.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine whether the caller supplied an explicit --workflow flag
			// (either on this command or the parent workflow group).
			workflowFlagSet := cmd.Flags().Changed("workflow") || cmd.InheritedFlags().Changed("workflow")

			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			// Collision guard: if we resolved the path via walk-up (no explicit
			// --workflow flag) and the resolved path already exists, fail closed.
			// Walk-up can silently land in the WRONG feat dir when multiple feat
			// dirs exist under docs/browzer/. Require an explicit path in that case
			// to prevent silent collisions.
			if !workflowFlagSet {
				if _, statErr := os.Stat(wfPath); statErr == nil {
					return fmt.Errorf(
						"workflow init: walk-up resolved to existing %s — pass --workflow docs/browzer/<new-feat>/workflow.json explicitly to avoid collision",
						wfPath,
					)
				}
			}
			switch executionStrategy {
			case "", "serial", "parallel", "parallel-worktrees", "agent-teams":
			default:
				return fmt.Errorf("workflow init: invalid --execution-strategy %q (want serial|parallel|parallel-worktrees|agent-teams)", executionStrategy)
			}
			// F-8: validate --mode flag.
			switch mode {
			case "", "autonomous", "review":
			default:
				return fmt.Errorf("workflow init: invalid --mode %q (want autonomous|review)", mode)
			}
			if mode == "" {
				mode = "autonomous"
			}
			opts := wf.BootstrapOptions{
				FeatureID:         featureID,
				FeatureName:       featureName,
				OriginalRequest:   originalRequest,
				OperatorLocale:    operatorLocale,
				ExecutionStrategy: executionStrategy,
				Mode:              mode,
			}
			err = wf.BootstrapSkeleton(wfPath, opts)
			if err != nil {
				if errors.Is(err, wf.ErrAlreadyExists) && !force {
					return fmt.Errorf(
						"workflow init: %s already exists (pass --force to overwrite)",
						wfPath,
					)
				}
				if errors.Is(err, wf.ErrAlreadyExists) && force {
					_, _ = fmt.Fprintf(
						cmd.ErrOrStderr(),
						"warn: --force is not implemented; refusing to overwrite %s\n",
						wfPath,
					)
					return fmt.Errorf("workflow init: refused to overwrite %s", wfPath)
				}
				return err
			}
			if !quietByDefaultUnderLLM(cmd) {
				_, _ = fmt.Fprintf(
					cmd.OutOrStdout(),
					"workflow init: seeded %s (featureId=%s)\n",
					wfPath, featureID,
				)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&featureID, "feature-id", "", "feature id (default: derive from feat-<slug> dir name)")
	cmd.Flags().StringVar(&featureName, "feature-name", "", "human-readable feature label")
	cmd.Flags().StringVar(&originalRequest, "original-request", "", "operator's verbatim ask")
	cmd.Flags().StringVar(&operatorLocale, "operator-locale", "en-US", "operator locale (e.g. en-US, pt-BR)")
	cmd.Flags().StringVar(&executionStrategy, "execution-strategy", "", "seed config.executionStrategy (serial|parallel|parallel-worktrees|agent-teams)")
	cmd.Flags().StringVar(&mode, "mode", "autonomous", "seed config.mode (autonomous|review)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing workflow.json (currently rejected — keep this safe by default)")
	parent.AddCommand(cmd)
}
