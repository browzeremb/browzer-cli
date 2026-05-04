package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// workflowCmd is the parent cobra command for all `browzer workflow` subcommands.
var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Inspect and validate workflow.json files",
	Long: "Read and validate Browzer feature workflow.json files.\n" +
		"\n" +
		"Mutator verbs (acquire advisory lock + validate post-mutation):\n" +
		"  append-step, update-step, complete-step, set-status, set-config,\n" +
		"  set-current-step, append-review-history, append-dispatch,\n" +
		"  audit-model-override, truncation-audit, reapply-additional-context,\n" +
		"  patch --jq <expr> (generic; supports --arg KEY=VAL / --argjson KEY=<json>).\n" +
		"\n" +
		"Read verbs (no lock):\n" +
		"  get-step [--field <jq-path>] [--render <template>] [--bash-vars]\n" +
		"           [--save <path>] [--quiet]\n" +
		"  get-config <key>, validate, schema [--json-schema], query <named>,\n" +
		"  describe-step-type <NAME>.\n" +
		"\n" +
		"Quiet modes (silence the per-mutation audit line on stderr — errors\n" +
		"and structured hints still print):\n" +
		"  --quiet                       flag, persistent across the workflow group\n" +
		"  BROWZER_WORKFLOW_QUIET=1      env var, equivalent to --quiet\n" +
		"  --llm  / BROWZER_LLM=1        also strips banners + ANSI + spinners;\n" +
		"                                audit line routes to the SQLite tracker\n" +
		"                                (workflow-audit:llm-*) so `browzer gain`\n" +
		"                                aggregation continues to work.\n" +
		"\n" +
		"There is no `patch step` / `patch <stepId>` verb. Generic mutations of\n" +
		"a step's payload always go through `patch --jq '<expr>'`.\n" +
		"\n" +
		"Run `browzer workflow [command] --help` for subcommand details.",
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
	// Write-mode flags. Mutually exclusive: --async, --sync, --await. When
	// none is set, resolveWriteMode falls through to config + default. Read
	// verbs ignore these flags.
	cmd.PersistentFlags().Bool("async", false, "send mutation through the daemon and return immediately (default mode)")
	cmd.PersistentFlags().Bool("sync", false, "skip the daemon and apply the mutation in-process (historic behaviour)")
	cmd.PersistentFlags().Bool("await", false, "send mutation through the daemon and block until durable (file + parent dir fsync)")
	// --quiet suppresses the per-mutation audit line on success. Errors and
	// fallback warnings still print on stderr (the exit code remains the
	// authoritative success signal). Also honored via BROWZER_WORKFLOW_QUIET
	// or BROWZER_LLM/--llm so LLM-driven shells don't pollute the agent's
	// tool-result context with high-frequency telemetry. On read verbs,
	// --quiet only silences the post-save confirmation line emitted under
	// --save; the data payload itself is never silenced.
	cmd.PersistentFlags().Bool("quiet", false, "suppress the audit telemetry line on success (errors still print)")
	// --no-schema-check (TASK_02 / WF-SYNC-1) bypasses the CUE-based
	// schema validator on the standalone write path. Bypass writes one
	// line (timestamp + sha256 + verb + path) to
	// `<repo-root>/.browzer/audit/no-schema-check.log`. Daemon path
	// always validates today; TASK_06 plumbs the bypass through the JSON-RPC
	// surface.
	cmd.PersistentFlags().Bool("no-schema-check", false, "bypass CUE schema validation (writes audit line to .browzer/audit/no-schema-check.log)")
	registerWorkflowAppendDispatch(cmd)
	registerWorkflowAppendReviewHistory(cmd)
	registerWorkflowAppendStep(cmd)
	registerWorkflowAuditModelOverride(cmd)
	registerWorkflowCompleteStep(cmd)
	registerWorkflowDescribeStepType(cmd)
	registerWorkflowGetConfig(cmd)
	registerWorkflowGetStep(cmd)
	registerWorkflowInit(cmd)
	registerWorkflowPatch(cmd)
	registerWorkflowQuery(cmd)
	registerWorkflowReapplyAdditionalContext(cmd)
	registerWorkflowSchema(cmd)
	registerWorkflowSetConfig(cmd)
	registerWorkflowSetCurrentStep(cmd)
	registerWorkflowSetStatus(cmd)
	registerWorkflowTruncationAudit(cmd)
	registerWorkflowUpdateStep(cmd)
	registerWorkflowValidate(cmd)

	parent.AddCommand(cmd)
}

// getWorkflowPath resolves the workflow.json path for the given command using
// the --workflow flag, BROWZER_WORKFLOW env, or git-style walk-up. The result
// is ALWAYS absolute — relative paths leak into the daemon RPC and trip the
// `path_must_be_absolute` guard in internal/daemon/methods.go (the standalone
// fallback tolerates relative, but the daemon's stricter contract is the
// authoritative one). Resolving here means every consumer (lock acquisition,
// mutator, audit line) sees the same canonical path regardless of CWD.
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
	resolved, err := wf.ResolveWorkflowPath(flagPath, cwd, cmd.Root().ErrOrStderr())
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(resolved) {
		return resolved, nil
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("absolute path: %w", err)
	}
	return abs, nil
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
