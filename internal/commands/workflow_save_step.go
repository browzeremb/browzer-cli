package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/schema"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/browzeremb/browzer-cli/internal/workflow/view"
	"github.com/spf13/cobra"
)


// registerSaveStep wires the top-level `browzer save-step <PHASE> --id <feat>`
// command. Callers (skills, the orchestrator, operators) invoke
// `browzer save-step <PHASE> --from <staged-file>` directly. The
// PostToolUse(Write) autosave bridge from
// `packages/skills/hooks/_auto-save-step.mjs` was retired in v5.0.0.
func registerSaveStep(root *cobra.Command) {
	var (
		idFlag       string
		workflowFlag string
		fromFile     string
		fromStdin    bool
		format       string
		validateOnly bool
		quiet        bool
		hintFixes    bool
		noHintFixes  bool
		noBackup     bool
	)

	cmd := &cobra.Command{
		Use:   "save-step <PHASE>",
		Short: "Persist a phase artefact into workflow.json (CUE-validated)",
		Long: `Persist the parsed payload at the workflow.json slot for <PHASE>.

PHASE accepts: PRD, TASKS / TASKS_MANIFEST (aliases), TASK_NN (zero-padded),
CODE_REVIEW, RECEIVING_CODE_REVIEW, WRITE_TESTS, UPDATE_DOCS,
FEATURE_ACCEPTANCE, COMMIT, BRAINSTORM / BRAINSTORMING (aliases).

Input source (one is required):
  --from <file>     read file; format inferred from extension
                    (.md → markdown PRD parser; .json → JSON)
  --stdin           read stdin; --format md|json (default json)

Behaviour:
  - Upsert step at the phase slot (creates if missing).
  - For TASK_NN: locate by stepId; set task.execution; status=COMPLETED.
  - Validates the post-mutation document against the CUE schema BEFORE
    writing. Validation failure: exit 2 + structured stderr.
  - --validate-only runs validation only.
  - --hint-fixes: on enum or unknown-field violations, emit a worked
    example showing a valid value, e.g.
    expected one of [a, b, c]; example: "<key>": "<a>"
  - Honors BROWZER_WORKFLOW_MODE=sync|await (--async is not accepted; use --await or --sync).
  - Stdout silent on success; --quiet (default under BROWZER_LLM=1)
    suppresses the success line on stderr.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			phase := strings.TrimSpace(args[0])
			if phase == "" {
				return cliExitErr(2, errors.New("phase is required"))
			}

			if (fromFile == "") == (!fromStdin) {
				return cliExitErr(2, errors.New("exactly one of --from / --stdin is required"))
			}

			if idFlag == "" && workflowFlag == "" {
				return cliExitErr(2, errors.New("--id or --workflow is required"))
			}

			// FR-5: BROWZER_LLM=1 implies --hint-fixes unless --no-hint-fixes is set.
			effectiveHintFixes := resolveHintFixes(hintFixes, noHintFixes)

			var path string
			if workflowFlag != "" && idFlag == "" {
				abs, absErr := filepath.Abs(workflowFlag)
				if absErr != nil {
					return cliExitErr(2, fmt.Errorf("--workflow: resolve %q: %w", workflowFlag, absErr))
				}
				path = abs
			} else {
				var pathErr error
				path, pathErr = resolveWorkflowPathForGetSave(cmd, idFlag, true)
				if pathErr != nil {
					return cliExitErr(2, pathErr)
				}
			}

			payload, srcFormat, err := readSaveInput(cmd, fromFile, format)
			if err != nil {
				return cliExitErr(2, err)
			}

			parsed, err := parsePayload(payload, srcFormat, phase)
			if err != nil {
				return cliExitErr(2, fmt.Errorf("parse %s payload: %w", phase, err))
			}

			// FR-4: wrapper-vs-body hint — detect before CUE validation so we
			// can emit a clear error pointing at the submission mistake.
			if effectiveHintFixes {
				if hint := detectWrapperVsBodyHint(phase, parsed); hint != "" {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), hint)
					return cliExitErr(2, errors.New("save-step: wrapper-vs-body mismatch detected"))
				}
			}

			// NoBackup is later overlaid from the cmd flag inside
			// dispatchToDaemonOrFallback via flagBoolEither("no-backup");
			// keep the inline write only for the validate-only / dry-run
			// branch which doesn't traverse that overlay.
			args2 := wf.MutatorArgs{
				Args:     []string{phase},
				Payload:  mustJSON(parsed),
				NoBackup: noBackup,
			}

			if validateOnly {
				dr, err := wf.ApplyDryRun(path, "save-step", args2)
				if err != nil {
					return cliExitErr(2, err)
				}
				if !dr.Ok {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", strings.Join(dr.Errors, "\n"))
					return cliExitErr(2, fmt.Errorf("validation failed (%d violations)", len(dr.Errors)))
				}
				if !shouldSuppressSaveSuccess(cmd, quiet) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "save-step: validation OK (no write)")
				}
				return nil
			}

			noLock, _ := cmd.Flags().GetBool("no-lock")
			lockTimeout := 5 * time.Second
			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return cliExitErr(2, err)
			}
			// save-step defaults to durable (await) — the very next agent
			// command typically reads the persisted step (e.g. orchestrator
			// running get-step right after the autosave hook fires). The
			// generic mutator default is fire-and-forget; save-step is
			// read-after-write. Operators can still force --sync explicitly.
			sync, _ := cmd.Flags().GetBool("sync")
			await, _ := cmd.Flags().GetBool("await")
			envMode := os.Getenv("BROWZER_WORKFLOW_MODE")
			if !sync && !await && envMode == "" {
				mode = writeModeDaemonSync
			}
			if err := dispatchToDaemonOrFallback(cmd, path, "save-step", args2, mode, noLock, lockTimeout); err != nil {
				// When --hint-fixes is set (or implied by BROWZER_LLM=1), re-run
				// validation so we can surface richer error messages with worked examples.
				if effectiveHintFixes {
					emitHintFixesOnError(cmd, path, args2)
				}
				return enrichSaveStepTaskNotFound(phase, idFlag, err)
			}
			if !shouldSuppressSaveSuccess(cmd, quiet) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "save-step: persisted %s → %s\n", phase, path)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&idFlag, "id", "", "feature id; resolves docs/browzer/<id>/workflow.json (alternative to --workflow)")
	cmd.Flags().StringVar(&workflowFlag, "workflow", "", "explicit workflow.json path (alternative to --id)")
	cmd.Flags().StringVar(&fromFile, "from", "", "read payload from file (.md or .json — format inferred from extension)")
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "read payload from stdin (use --format md|json)")
	cmd.Flags().StringVar(&format, "format", "json", "stdin format: md or json (default json)")
	cmd.Flags().BoolVar(&validateOnly, "validate-only", false, "validate the parsed payload without writing")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress the success line on stderr")
	cmd.Flags().BoolVar(&hintFixes, "hint-fixes", false, "on enum/unknown-field violations emit a worked example showing a valid value")
	cmd.Flags().BoolVar(&noHintFixes, "no-hint-fixes", false, "suppress hint-fixes even when BROWZER_LLM=1 implies it")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "skip backup rotation before write (opt-in; default rotates up to 2 backups)")
	// Honor the global write-mode flags (defined on the workflow group). Save
	// is a top-level command, so re-declare here.
	cmd.Flags().Bool("sync", false, "skip daemon, apply in-process")
	cmd.Flags().Bool("await", false, "send via daemon, block until durable")
	cmd.Flags().Bool("no-lock", false, "skip advisory lock")
	cmd.Flags().Bool("no-schema-check", false, "bypass CUE validation (audited)")
	root.AddCommand(cmd)

	// Register the --batch variant.
	registerSaveStepBatch(root)
}

// emitHintFixesOnError runs the full dry-run validation pipeline and writes
// the richer --hint-fixes messages (aggregated diagnostic table + worked
// examples) to stderr. Called by the save-step RunE when --hint-fixes is set
// and the write failed.
//
// Emits in two parts:
//  1. An aggregated diagnostic table (path | kind | got | expected) that
//     surfaces ALL violations at once (FR-2).
//  2. The per-violation hint lines from FormatViolationsWithHints (FR-3).
func emitHintFixesOnError(cmd *cobra.Command, path string, args2 wf.MutatorArgs) {
	res, err := wf.DryRunViolations(path, "save-step", args2)
	if err != nil || res == nil || res.Valid || len(res.Violations) == 0 {
		return
	}

	// FR-2: aggregated diagnostic table.
	rows := BuildDiagnosticTable(res.Violations)
	if table := FormatDiagnosticTable(rows); table != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "violations (%d):\n%s\n", len(res.Violations), table)
	}

	// FR-3: per-violation worked examples (enum / unknown-field only).
	hints := schema.FormatViolationsWithHints(res.Violations)
	if hints != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "hint-fixes:\n%s\n", hints)
	}
}

// registerSaveStepBatch wires `browzer save-step --batch --from PHASE=PATH ...`
// which persists multiple phases atomically in a single CUE-validated fsync.
//
// All phases are validated together before any write (single CUE pass).
// On any individual phase parse or validation failure the entire batch is
// rolled back — workflow.json mtime is unchanged.
func registerSaveStepBatch(root *cobra.Command) {
	var (
		idFlag       string
		workflowFlag string
		fromPairs    []string
		quiet        bool
		hintFixes    bool
		noHintFixes  bool
		noBackup     bool
	)

	cmd := &cobra.Command{
		Use:   "save-step-batch",
		Short: "Persist multiple phase artefacts atomically (CUE-validated single fsync)",
		Long: `Persist multiple phase artefacts into workflow.json atomically.

All listed phases are validated in a single CUE pass BEFORE any write.
On failure the entire batch is rolled back and workflow.json is unchanged.

  --from PHASE=PATH   phase name and the .json (or .md for PRD) file to
                      read. Repeat for each phase in the batch.

Examples:
  browzer save-step-batch --id feat-123 \
    --from PRD=staging/PRD.md \
    --from TASKS_MANIFEST=staging/TASKS_MANIFEST.json

  browzer save-step --batch --id feat-123 \
    --from PRD=staging/PRD.md \
    --from TASKS_MANIFEST=staging/TASKS_MANIFEST.json

Flags --sync / --await / --no-lock / --no-schema-check are honored per
phase. --hint-fixes emits worked examples on enum/unknown-field violations.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(fromPairs) == 0 {
				return cliExitErr(2, errors.New("--batch requires at least one --from PHASE=PATH pair"))
			}

			if idFlag == "" && workflowFlag == "" {
				return cliExitErr(2, errors.New("--id or --workflow is required"))
			}

			// FR-5: BROWZER_LLM=1 implies --hint-fixes unless --no-hint-fixes is set.
			effectiveHintFixes := resolveHintFixes(hintFixes, noHintFixes)

			var wfPath string
			if workflowFlag != "" && idFlag == "" {
				abs, absErr := filepath.Abs(workflowFlag)
				if absErr != nil {
					return cliExitErr(2, fmt.Errorf("--workflow: resolve %q: %w", workflowFlag, absErr))
				}
				wfPath = abs
			} else {
				var pathErr error
				wfPath, pathErr = resolveWorkflowPathForGetSave(cmd, idFlag, true)
				if pathErr != nil {
					return cliExitErr(2, pathErr)
				}
			}

			// Parse all PHASE=PATH pairs upfront before touching disk.
			// F-3: detect duplicate phase names immediately — last-wins is silent
			// and produces confusing results; fail loudly instead.
			seenPhases := make(map[string]bool)
			batch := make([]wf.BatchMutatorArgs, 0, len(fromPairs))
			phaseNames := make([]string, 0, len(fromPairs))
			for _, pair := range fromPairs {
				sep := strings.Index(pair, "=")
				if sep <= 0 {
					return cliExitErr(2, fmt.Errorf("--from %q: expected PHASE=PATH format", pair))
				}
				phase := strings.TrimSpace(pair[:sep])
				filePath := strings.TrimSpace(pair[sep+1:])
				if phase == "" || filePath == "" {
					return cliExitErr(2, fmt.Errorf("--from %q: phase and path must both be non-empty", pair))
				}
				if seenPhases[phase] {
					return cliExitErr(2, fmt.Errorf("duplicate phase %q in batch --from pairs", phase))
				}
				seenPhases[phase] = true

				rawBytes, srcFmt, err := readSaveInput(cmd, filePath, "json")
				if err != nil {
					return cliExitErr(2, fmt.Errorf("batch --from %s: %w", phase, err))
				}
				parsed, err := parsePayload(rawBytes, srcFmt, phase)
				if err != nil {
					return cliExitErr(2, fmt.Errorf("batch parse %s: %w", phase, err))
				}
				// FR-4: wrapper-vs-body hint per phase.
				if effectiveHintFixes {
					if hint := detectWrapperVsBodyHint(phase, parsed); hint != "" {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), hint)
						return cliExitErr(2, fmt.Errorf("save-step-batch: wrapper-vs-body mismatch in phase %s", phase))
					}
				}
				batch = append(batch, wf.BatchMutatorArgs{
					Verb: "save-step",
					Args: wf.MutatorArgs{
						Args:     []string{phase},
						Payload:  mustJSON(parsed),
						NoBackup: noBackup,
					},
				})
				phaseNames = append(phaseNames, phase)
			}

			// Acquire one advisory lock across the entire batch (F-1 / F-4).
			noLock, _ := cmd.Flags().GetBool("no-lock")
			lockTimeout := 10 * time.Second
			lock, _, lockErr := acquireMutatorLock(cmd, wfPath, noLock, lockTimeout)
			if lockErr != nil {
				if lockErr == wf.ErrLockTimeout {
					return errLockTimeoutExitCode
				}
				return lockErr
			}
			if lock != nil {
				defer func() { _ = lock.Release() }()
			}

			// Apply all mutators sequentially under the single lock, validate
			// ONCE post-batch, and write ONCE (F-1 / F-2 / F-4).
			_, batchErr := wf.ApplyBatchAndPersist(wfPath, batch)
			if batchErr != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "batch write error: %v\n", batchErr)
				if effectiveHintFixes {
					// F-9: single DryRunViolations call instead of ApplyDryRun + DryRunViolations.
					for i, b := range batch {
						if vres, verr := wf.DryRunViolations(wfPath, b.Verb, b.Args); verr == nil && vres != nil && !vres.Valid {
							if hints := schema.FormatViolationsWithHints(vres.Violations); hints != "" {
								_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "hint-fixes [%s]:\n%s\n", phaseNames[i], hints)
							}
						}
					}
				}
				return cliExitErr(2, batchErr)
			}

			if !shouldSuppressSaveSuccess(cmd, quiet) {
				for _, phase := range phaseNames {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "save-step: persisted %s → %s\n", phase, wfPath)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&idFlag, "id", "", "feature id; resolves docs/browzer/<id>/workflow.json (alternative to --workflow)")
	cmd.Flags().StringVar(&workflowFlag, "workflow", "", "explicit workflow.json path (alternative to --id)")
	cmd.Flags().StringArrayVar(&fromPairs, "from", nil, "PHASE=PATH pair; repeat for each phase (required)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress per-phase success lines on stderr")
	cmd.Flags().BoolVar(&hintFixes, "hint-fixes", false, "on enum/unknown-field violations emit a worked example")
	cmd.Flags().BoolVar(&noHintFixes, "no-hint-fixes", false, "suppress hint-fixes even when BROWZER_LLM=1 implies it")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "skip backup rotation before write (opt-in; default rotates up to 2 backups)")
	cmd.Flags().Bool("sync", false, "skip daemon, apply in-process")
	cmd.Flags().Bool("await", false, "send via daemon, block until durable")
	cmd.Flags().Bool("no-lock", false, "skip advisory lock")
	cmd.Flags().Bool("no-schema-check", false, "bypass CUE validation (audited)")
	root.AddCommand(cmd)
}

// resolveHintFixes implements FR-5: BROWZER_LLM=1 (or BROWZER_LLM=true, case-
// insensitive) implies --hint-fixes unless --no-hint-fixes is explicitly set.
// The --no-hint-fixes flag is the hard override; --hint-fixes is additive.
func resolveHintFixes(hintFixes, noHintFixes bool) bool {
	if noHintFixes {
		return false
	}
	if hintFixes {
		return true
	}
	// BROWZER_LLM=1 implies hint-fixes.
	return envBoolish(os.Getenv("BROWZER_LLM"))
}

// wrapperOnlyFields are the fields that appear in the full step wrapper but not
// in any body payload. Presence of one or more of these alongside a body key
// signals the caller submitted the full step wrapper instead of just the body.
var wrapperOnlyFields = []string{
	"applicability", "stepId", "startedAt", "status", "completedAt", "owner", "worktrees",
}

// bodyKeys are the payload sub-keys that hold the body for each phase type.
// If the input contains one of these AND has an outer `name` + wrapper-only
// field, it is definitely a wrapper-vs-body mistake.
var bodyKeys = []string{
	"prd", "tasksManifest", "task", "codeReview", "receivingCodeReview",
	"writeTests", "updateDocs", "featureAcceptance", "commit", "brainstorming",
}

// detectWrapperVsBodyHint returns a non-empty error string when the submitted
// JSON appears to be the full step wrapper instead of just the body payload.
// It checks (all three conditions must hold):
//  1. The input has an outer `name` field matching the phase argument.
//  2. The input has at least one canonical wrapper-only field.
//  3. The input has a body-typed sub-key (the nested body payload).
//
// FR-4 (retro2): surface only when --hint-fixes / BROWZER_LLM=1 is in effect
// (the caller must gate on resolveHintFixes before calling this function).
func detectWrapperVsBodyHint(phase string, m map[string]any) string {
	if m == nil {
		return ""
	}
	nameVal, _ := m["name"].(string)
	if nameVal != phase {
		return ""
	}
	// Check for at least one wrapper-only field.
	hasWrapper := false
	for _, wrapperField := range wrapperOnlyFields {
		if _, ok := m[wrapperField]; ok {
			hasWrapper = true
			break
		}
	}
	if !hasWrapper {
		return ""
	}
	// Check for a body key that carries the actual payload.
	bodyKey := ""
	for _, bk := range bodyKeys {
		if _, ok := m[bk]; ok {
			bodyKey = bk
			break
		}
	}
	if bodyKey == "" {
		return ""
	}
	// Build a concise human-readable representation of the wrapper shape.
	outer := fmt.Sprintf(`{"name": %q, "applicability": ..., %q: {...}, ...}`, phase, bodyKey)
	return fmt.Sprintf(
		"save-step: input appears to wrap the body in the full step. Submit only the BODY (the `%s` payload), not the full step wrapper.\n\n  Got:    %s\n  Expected: {<the contents of %s>}\n\nThe CLI wraps the body itself. See `browzer workflow describe-step-type %s` for the body shape.",
		bodyKey, outer, bodyKey, phase,
	)
}

// enrichSaveStepTaskNotFound adds an actionable hint when save-step fails with
// "step not found" for a TASK_NN phase. This happens when TASKS_MANIFEST was
// not persisted first — the TASK_NN slots are materialised by save-step
// TASKS_MANIFEST, not by append-step.
func enrichSaveStepTaskNotFound(phase, idFlag string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	isTaskNN := regexp.MustCompile(`^TASK_\d+$`).MatchString(strings.ToUpper(phase))
	if !isTaskNN || !strings.Contains(msg, "not found") {
		return err
	}
	hint := "hint: TASK_NN slots are created by `browzer save-step TASKS_MANIFEST`"
	if idFlag != "" {
		hint += fmt.Sprintf(" — verify TASKS_MANIFEST was saved: browzer get-step TASKS_MANIFEST --id %s", idFlag)
	} else {
		hint += " — verify the preceding save-step TASKS_MANIFEST succeeded"
	}
	return fmt.Errorf("%w\n%s", err, hint)
}

func shouldSuppressSaveSuccess(cmd *cobra.Command, quietFlag bool) bool {
	if quietFlag {
		return true
	}
	return quietByDefaultUnderLLM(cmd)
}

// readSaveInput resolves the payload source and returns (payload, format).
// Format is "md" or "json" (lowercased).
// When fromFile is non-empty, the file is read; otherwise stdin is consumed.
func readSaveInput(cmd *cobra.Command, fromFile string, format string) ([]byte, string, error) {
	if fromFile != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return nil, "", fmt.Errorf("read %q: %w", fromFile, err)
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fromFile), "."))
		switch ext {
		case "md", "markdown":
			return data, "md", nil
		case "json":
			return data, "json", nil
		default:
			return nil, "", fmt.Errorf("unsupported file extension %q (want .md or .json)", ext)
		}
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil, "", fmt.Errorf("read stdin: %w", err)
	}
	f := strings.ToLower(strings.TrimSpace(format))
	if f == "" {
		f = "json"
	}
	if f != "md" && f != "json" {
		return nil, "", fmt.Errorf("--format must be md or json, got %q", f)
	}
	return data, f, nil
}

// parsePayload normalises raw input to a generic JSON map.
func parsePayload(raw []byte, srcFormat, phase string) (map[string]any, error) {
	switch srcFormat {
	case "json":
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil {
			if phase == "TASKS" || phase == "TASKS_MANIFEST" {
				sanitizeTasksManifest(m)
			}
			return m, nil
		}
		// Fallback: TASKS / TASKS_MANIFEST historically accepted a bare
		// array of task briefs from the generate-task skill. Auto-wrap
		// it into the #TasksManifest shape so the CUE schema accepts it.
		if phase == "TASKS" || phase == "TASKS_MANIFEST" {
			var arr []any
			if err := json.Unmarshal(raw, &arr); err == nil {
				return wrapTasksArray(arr), nil
			}
		}
		// Re-attempt strict object decode to surface the original error.
		var m2 map[string]any
		return nil, json.Unmarshal(raw, &m2)
	case "md":
		// Markdown is only meaningful for PRD today; other phases require JSON.
		if phase != "PRD" {
			return nil, fmt.Errorf("markdown input is only supported for PRD (got phase=%s)", phase)
		}
		return parseMarkdownPRD(raw), nil
	}
	return nil, fmt.Errorf("unsupported source format %q", srcFormat)
}

// wrapTasksArray lifts a bare `[<TaskBrief>, ...]` array into the schema's
// #TasksManifest shape: `{totalTasks, tasksOrder, tasks}`. Each input entry
// is sanitised to the closed `#TaskBrief` field set; unknown fields are
// dropped; `tasksOrder` is derived from each `taskId`.
func wrapTasksArray(arr []any) map[string]any {
	tasks := make([]any, 0, len(arr))
	order := make([]string, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			tasks = append(tasks, item)
			continue
		}
		brief := sanitizeTaskBrief(m)
		tasks = append(tasks, brief)
		if id, ok := brief["taskId"].(string); ok && id != "" {
			order = append(order, id)
		}
	}
	return map[string]any{
		"totalTasks": len(tasks),
		"tasksOrder": order,
		"tasks":      tasks,
	}
}

// sanitizeTasksManifest mutates the tasks[] entries in-place, dropping
// fields not allowed by the closed #TaskBrief CUE definition.
func sanitizeTasksManifest(m map[string]any) {
	tasks, ok := m["tasks"].([]any)
	if !ok {
		return
	}
	for i, t := range tasks {
		if tm, ok := t.(map[string]any); ok {
			tasks[i] = sanitizeTaskBrief(tm)
		}
	}
	m["tasks"] = tasks
}

// sanitizeTaskBrief drops fields not allowed by the CUE `#TaskBrief`
// definition (the source of truth — see schemas/workflow-v1.cue). Adding
// a new field in CUE auto-propagates here. Beyond the allowlist filter,
// two coercions handle common agent drift:
//   - `scope` written as a struct `{files,...}` is coerced to the
//     schema's `[...string]` shape (we extract `.files`).
//   - `skillsFound` lifted from a nested `explorer.skillsFound` shape
//     into the flat brief-level `skillsFound`.
func sanitizeTaskBrief(m map[string]any) map[string]any {
	allowed, err := schema.AllowedFields("#TaskBrief")
	if err != nil || len(allowed) == 0 {
		// Defensive: missing schema → return input unchanged so a stale
		// or broken embed doesn't silently strip everything.
		return m
	}
	out := map[string]any{}
	for k, v := range m {
		if !allowed[k] {
			continue
		}
		out[k] = v
	}
	// Coercion: scope struct → []string.
	switch sv := m["scope"].(type) {
	case []any:
		out["scope"] = sv
	case map[string]any:
		if files, ok := sv["files"].([]any); ok && allowed["scope"] {
			out["scope"] = files
		}
	}
	// Coercion: pull skillsFound up from explorer when missing flat.
	if _, hasFlat := out["skillsFound"]; !hasFlat && allowed["skillsFound"] {
		if exp, ok := m["explorer"].(map[string]any); ok {
			if sf, ok := exp["skillsFound"].([]any); ok {
				out["skillsFound"] = stringifySkills(sf)
			}
		}
	} else if sf, ok := out["skillsFound"].([]any); ok {
		out["skillsFound"] = stringifySkills(sf)
	}
	return out
}

// stringifySkills coerces a `skillsFound` list to []string, accepting either
// bare strings or `{skill: ..., domain: ..., relevance: ...}` objects (the
// richer #SkillFound shape used inside #TaskExplorer).
func stringifySkills(in []any) []any {
	out := make([]any, 0, len(in))
	for _, item := range in {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if name, ok := v["skill"].(string); ok && name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

var (
	frontmatterRE = regexp.MustCompile(`(?s)\A---\n(.+?)\n---\n`)
	h1RE          = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	headingRE     = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
	bulletRE      = regexp.MustCompile(`(?m)^\s*[-*]\s+(.+?)\s*$`)
)

var prdPriorityWords = map[string]bool{"must": true, "should": true, "could": true}

// parseMarkdownPRD extracts the #PRD payload from a markdown document.
//
// Recognised structure (case-insensitive H2 names):
//
//	## Title (or H1 "# <title>")            → string title
//	## Overview / ## Summary                → string overview
//	## Personas                             → [{id: P-N, description}]
//	## Objectives                           → [string]
//	## In Scope                             → [string]
//	## Out of Scope                         → [string]
//	## Deliverables                         → [string]
//	## Functional Requirements              → [{id: FR-N, priority?, text}]
//	## Non-Functional Requirements / NFRs   → [{id: NFR-N, category?, target, text}]
//	## Success Metrics                      → [{id: M-N, metric, target, method}]
//	## Acceptance Criteria                  → [{id: AC-N, bindsTo?, text}]
//	## Risks                                → [{id: R-N, text, mitigation}]
//	## Dependencies                         → {external?: [string], internal?: [string]}
//	## Assumptions                          → [string]
//	## Task Granularity                     → string
//
// ID-prefixed bullet patterns (one bullet per item):
//
//	- FR-1 (must) text…
//	- AC-1 (FR-1, FR-2) text…
//	- NFR-1 (category) target=<T> text…   (target also accepted as `target: <T>`)
//	- M-1: <metric> → <target> (<method>)  OR  - M-1 metric=<m>; target=<t>; method=<m>
//	- P-1: description
//	- R-1: text — mitigation: <mit>
//
// Unknown H2 sections are dropped silently — the CUE schema does not allow
// `extraSections` under #PRD, and surfacing them would always fail validation.
// Frontmatter (`---` JSON-ish block) is best-effort; failures fall through.
func parseMarkdownPRD(raw []byte) map[string]any {
	out := map[string]any{}
	body := string(raw)

	if m := frontmatterRE.FindStringSubmatch(body); m != nil {
		var fm map[string]any
		if err := json.Unmarshal([]byte("{"+m[1]+"}"), &fm); err == nil {
			maps.Copy(out, fm)
		}
		body = body[len(m[0]):]
	}

	if m := h1RE.FindStringSubmatch(body); m != nil {
		title := strings.TrimSpace(m[1])
		for _, prefix := range []string{"PRD —", "PRD -", "PRD:"} {
			if rest, ok := strings.CutPrefix(title, prefix); ok {
				title = strings.TrimSpace(rest)
				break
			}
		}
		if title != "" {
			out["title"] = title
		}
	}

	matches := headingRE.FindAllStringSubmatchIndex(body, -1)
	for i, m := range matches {
		title := strings.ToLower(strings.TrimSpace(body[m[2]:m[3]]))
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		section := strings.TrimSpace(body[m[1]:end])

		switch title {
		case "title":
			if t := proseFromSection(section); t != "" {
				out["title"] = t
			}
		case "summary", "overview":
			out["overview"] = proseFromSection(section)
		case "objectives":
			out["objectives"] = bulletsToList(section)
		case "in scope":
			out["inScope"] = bulletsToList(section)
		case "out of scope":
			out["outOfScope"] = bulletsToList(section)
		case "deliverables":
			out["deliverables"] = bulletsToList(section)
		case "assumptions":
			out["assumptions"] = bulletsToList(section)
		case "task granularity":
			out["taskGranularity"] = proseFromSection(section)
		case "personas":
			out["personas"] = parseIDBullets(section, "P")
		case "functional requirements", "frs":
			out["functionalRequirements"] = parseIDBullets(section, "FR")
		case "non-functional requirements", "nfrs":
			out["nonFunctionalRequirements"] = parseIDBullets(section, "NFR")
		case "success metrics":
			out["successMetrics"] = parseIDBullets(section, "M")
		case "acceptance criteria":
			out["acceptanceCriteria"] = parseIDBullets(section, "AC")
		case "risks":
			out["risks"] = parseIDBullets(section, "R")
		case "dependencies":
			if deps := parseDependencies(section); len(deps) > 0 {
				out["dependencies"] = deps
			}
		default:
			// Drop unknown sections silently — the CUE schema does not
			// allow extraSections under #PRD. Operator-side reviewers can
			// `grep '## '` if they need to confirm coverage.
		}
	}
	return out
}

// bulletsToList collects single-line bullets in a section as a string slice,
// preserving order and stripping the `- ` / `* ` prefix.
func bulletsToList(s string) []string {
	matches := bulletRE.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		t := strings.TrimSpace(m[1])
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// proseFromSection flattens non-bullet content into a single space-joined
// string. Bullet lines are skipped — sections like Overview should be prose,
// not lists.
func proseFromSection(s string) string {
	lines := strings.Split(s, "\n")
	keep := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") {
			continue
		}
		keep = append(keep, t)
	}
	return strings.Join(keep, " ")
}

// parseIDBullets walks every bullet in section, projecting the ones whose body
// starts with the given <kind>-N prefix into structured maps. Bullets that
// don't match are dropped.
func parseIDBullets(section, kind string) []map[string]any {
	bullets := bulletsToList(section)
	out := make([]map[string]any, 0, len(bullets))
	for _, b := range bullets {
		m := parseIDBullet(b, kind)
		if m == nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// parseIDBullet extracts a structured object from a single bullet body of
// the form `<kind>-N [(<attrs>)] [text…]`. Returns nil when the bullet
// doesn't begin with a matching ID.
func parseIDBullet(body, kind string) map[string]any {
	idRe := regexp.MustCompile(`^` + regexp.QuoteMeta(kind) + `-\d+`)
	loc := idRe.FindStringIndex(body)
	if loc == nil {
		return nil
	}
	id := body[loc[0]:loc[1]]
	rest := strings.TrimSpace(body[loc[1]:])
	out := map[string]any{"id": id}

	// Optional parenthesized attributes immediately after the ID.
	var attrs string
	if strings.HasPrefix(rest, "(") {
		if end := strings.Index(rest, ")"); end > 0 {
			attrs = strings.TrimSpace(rest[1:end])
			rest = strings.TrimSpace(rest[end+1:])
		}
	}

	rest = strings.TrimLeft(rest, ":—-")
	rest = strings.TrimSpace(rest)

	switch kind {
	case "FR":
		if attrs != "" {
			low := strings.ToLower(attrs)
			if prdPriorityWords[low] {
				out["priority"] = low
			}
		}
		if rest != "" {
			out["text"] = rest
		}
	case "AC":
		if attrs != "" {
			frs := []string{}
			for _, x := range splitAndTrim(attrs, ",") {
				if strings.HasPrefix(x, "FR-") {
					frs = append(frs, x)
				}
			}
			if len(frs) > 0 {
				out["bindsTo"] = frs
			}
		}
		if rest != "" {
			out["text"] = rest
		}
	case "NFR":
		if attrs != "" {
			out["category"] = attrs
		}
		if v, remaining := extractKV(rest, "target"); v != "" {
			out["target"] = v
			rest = remaining
		}
		if rest != "" {
			out["text"] = rest
		}
		// `target` is required by #NFR — when the agent forgot the
		// `target=` / `target:` keyword, default it to the bullet text so
		// CUE validation passes; the operator can refine afterwards.
		if _, ok := out["target"]; !ok {
			if t, ok := out["text"].(string); ok && t != "" {
				out["target"] = t
			} else {
				out["target"] = ""
			}
		}
	case "M":
		metric, rem := extractKV(rest, "metric")
		target, rem := extractKV(rem, "target")
		method, _ := extractKV(rem, "method")
		if metric != "" || target != "" || method != "" {
			out["metric"] = metric
			out["target"] = target
			out["method"] = method
			break
		}
		if strings.Contains(rest, "→") {
			parts := strings.SplitN(rest, "→", 2)
			out["metric"] = strings.TrimSpace(parts[0])
			right := strings.TrimSpace(parts[1])
			method := ""
			if i := strings.LastIndex(right, "("); i > 0 && strings.HasSuffix(right, ")") {
				method = strings.TrimSpace(right[i+1 : len(right)-1])
				right = strings.TrimSpace(right[:i])
			}
			out["target"] = right
			out["method"] = method
			break
		}
		out["metric"] = rest
		out["target"] = ""
		out["method"] = ""
	case "P":
		out["description"] = rest
	case "R":
		if v, remaining := extractKV(rest, "mitigation"); v != "" {
			out["mitigation"] = v
			rest = remaining
		}
		if rest != "" {
			out["text"] = rest
		}
		// `mitigation` is required by #Risk — same default-from-text rule
		// as #NFR.target.
		if _, ok := out["mitigation"]; !ok {
			if t, ok := out["text"].(string); ok && t != "" {
				out["mitigation"] = t
			} else {
				out["mitigation"] = ""
			}
		}
	}
	return out
}

// extractKV finds `<key>: <value>` or `<key>=<value>` in s (case-insensitive
// on the key) and returns the value plus s with that key/value pair removed.
// Value terminates at the first `; `, ` — `, ` - ` separator or end-of-string.
func extractKV(s, key string) (value, remaining string) {
	low := strings.ToLower(s)
	keyLow := strings.ToLower(key)
	for _, suffix := range []string{":", "="} {
		marker := keyLow + suffix
		idx := strings.Index(low, marker)
		if idx < 0 {
			continue
		}
		// Require the marker to start a token.
		if idx > 0 {
			// Require the marker to start a token: previous byte must be a
			// separator. Em-dash is multi-byte so check its trailing byte.
			prev := s[idx-1]
			if prev != ' ' && prev != '\t' && prev != ';' && prev != '-' && prev != '(' && prev != 0x94 {
				continue
			}
		}
		valStart := idx + len(marker)
		valEnd := len(s)
		sepLen := 0
		for _, sep := range []string{"; ", " — ", " - ", ": "} {
			if j := strings.Index(s[valStart:], sep); j >= 0 && valStart+j < valEnd {
				valEnd = valStart + j
				sepLen = len(sep)
				break // first (leftmost) separator wins; stop scanning
			}
		}
		val := strings.TrimSpace(s[valStart:valEnd])
		right := ""
		if valEnd+sepLen <= len(s) {
			right = s[valEnd+sepLen:]
		}
		left := strings.TrimRight(s[:idx], " \t;-")
		rem := strings.TrimSpace(left + " " + right)
		return val, rem
	}
	return "", s
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseDependencies recognises labelled bullets:
//
//	- external: foo, bar
//	- internal: baz
func parseDependencies(section string) map[string]any {
	out := map[string]any{}
	var ext, intl []string
	for _, b := range bulletsToList(section) {
		low := strings.ToLower(b)
		switch {
		case strings.HasPrefix(low, "external:"):
			ext = append(ext, splitAndTrim(b[len("external:"):], ",")...)
		case strings.HasPrefix(low, "internal:"):
			intl = append(intl, splitAndTrim(b[len("internal:"):], ",")...)
		}
	}
	if len(ext) > 0 {
		out["external"] = ext
	}
	if len(intl) > 0 {
		out["internal"] = intl
	}
	return out
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// =============================================================
// save-step mutator
// =============================================================

// init wires the save-step mutator into the shared registry. Done in init()
// so the package-level Mutators map (in apply.go) is amended without a
// circular import.
func init() {
	wf.Mutators["save-step"] = mutatorSaveStep
}

// mutatorSaveStep is the workflow.Mutator that powers `browzer save-step`.
// args.Args[0] is the phase (e.g. "PRD", "TASK_03"); args.Payload is the
// already-parsed payload as a JSON-encoded map.
func mutatorSaveStep(raw map[string]any, args wf.MutatorArgs, out *wf.ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("save-step: phase is required")
	}
	phase := args.Args[0]
	if len(args.Payload) == 0 {
		return fmt.Errorf("save-step: payload is required")
	}
	var payload map[string]any
	if err := json.Unmarshal(args.Payload, &payload); err != nil {
		return fmt.Errorf("save-step: parse payload: %w", err)
	}

	steps, _ := raw["steps"].([]any)
	now := time.Now().UTC().Format(time.RFC3339)

	switch {
	case strings.HasPrefix(phase, "TASK_") && phase != "TASKS":
		// Locate by taskId (the agent-facing identifier) — the real stepId
		// is `STEP_NN_TASK_MM` per the CUE regex on #StepBase.stepId.
		for i, s := range steps {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			tid, _ := sm["taskId"].(string)
			sid, _ := sm["stepId"].(string)
			if tid != phase && sid != phase {
				continue
			}
			task, _ := sm["task"].(map[string]any)
			if task == nil {
				task = map[string]any{}
			}
			mergeTaskPayload(task, payload)
			sm["task"] = task
			sm["status"] = "COMPLETED"
			if _, ok := sm["completedAt"]; !ok {
				sm["completedAt"] = now
			}
			steps[i] = sm
			out.StepID, _ = sm["stepId"].(string)
			raw["steps"] = steps
			wf.RecomputeCountersRaw(raw)
			return nil
		}
		return fmt.Errorf("save-step: TASK step %q not found (seed it via tasks-manifest save first)", phase)
	}

	stepName := canonicalStepName(phase)
	payloadKey := view.PayloadKey(stepName)
	if payloadKey == "" {
		return fmt.Errorf("save-step: unknown phase %q", phase)
	}

	// Upsert: find existing step by name; create if missing.
	var (
		stepIdx = -1
		stepMap map[string]any
	)
	for i, s := range steps {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := sm["name"].(string); n == stepName {
			stepIdx = i
			stepMap = sm
			break
		}
	}

	if stepMap == nil {
		stepMap = map[string]any{
			"stepId":    fmt.Sprintf("STEP_%02d_%s", len(steps)+1, stepName),
			"name":      stepName,
			"status":    "COMPLETED",
			"startedAt": now,
			"applicability": map[string]any{
				"applicable": true,
			},
		}
		stepMap[payloadKey] = payload
		stepMap["completedAt"] = now
		steps = append(steps, stepMap)
		out.StepID = stepMap["stepId"].(string)
	} else {
		stepMap[payloadKey] = payload
		stepMap["status"] = "COMPLETED"
		if _, ok := stepMap["completedAt"]; !ok {
			stepMap["completedAt"] = now
		}
		steps[stepIdx] = stepMap
		out.StepID, _ = stepMap["stepId"].(string)
	}

	raw["steps"] = steps

	// When the manifest lands, seed a stub TASK step per `tasksOrder` entry
	// so downstream `save-step TASK_NN` calls have a target step to upsert
	// into. Each stub copies scope + suggestedModel from the manifest's
	// task brief when present; gates start blank (specialist fills them).
	if stepName == "TASKS_MANIFEST" {
		seedTaskStubs(raw, payload, now)
		steps, _ = raw["steps"].([]any)
		_ = steps
	}

	// Recompute rollup counters (totalSteps, completedSteps, currentStepId,
	// updatedAt) after every successful save-step so the top-level summary
	// fields stay in sync with the actual steps[] state. FR-2 (retro2).
	wf.RecomputeCountersRaw(raw)
	return nil
}

// seedTaskStubs appends one PENDING TASK step per `tasksManifest.tasksOrder`
// entry that has no existing TASK_NN stepId yet. The stub copies the per-task
// brief from `tasksManifest.tasks[i]` when present (scope, suggestedModel,
// dependsOn) so the specialist sees the right working set on dispatch.
func seedTaskStubs(raw map[string]any, manifest map[string]any, now string) {
	rawOrder, _ := manifest["tasksOrder"].([]any)
	if len(rawOrder) == 0 {
		return
	}
	briefs := map[string]map[string]any{}
	if list, ok := manifest["tasks"].([]any); ok {
		for _, b := range list {
			if bm, ok := b.(map[string]any); ok {
				if id, _ := bm["taskId"].(string); id != "" {
					briefs[id] = bm
				}
			}
		}
	}
	steps, _ := raw["steps"].([]any)
	existingTask := map[string]bool{}
	for _, s := range steps {
		if sm, ok := s.(map[string]any); ok {
			if tid, _ := sm["taskId"].(string); tid != "" {
				existingTask[tid] = true
			}
		}
	}
	for _, idAny := range rawOrder {
		taskID, _ := idAny.(string)
		if taskID == "" || existingTask[taskID] {
			continue
		}
		// stepId follows the StepBase regex `^STEP_[0-9]{2}_[A-Z0-9_]+$`.
		// Sequence number is the post-append index; suffix mirrors taskId.
		stepID := fmt.Sprintf("STEP_%02d_%s", len(steps)+1, taskID)
		brief := briefs[taskID]
		taskBody := map[string]any{
			"scope":          []any{},
			"invariants":     []any{},
			"suggestedModel": nil,
			"trivial":        false,
			"execution": map[string]any{
				"gates":             map[string]any{"baseline": map[string]any{}, "postChange": map[string]any{}, "regression": []any{}},
				"scopeAdjustments":  []any{},
				"agents":            []any{},
				"invariantsChecked": []any{},
				"nextSteps":         "",
			},
		}
		if brief != nil {
			if v, ok := brief["scope"].([]any); ok {
				taskBody["scope"] = v
			}
			if v, ok := brief["title"].(string); ok && v != "" {
				taskBody["title"] = v
			}
			if v, ok := brief["suggestedModel"]; ok {
				taskBody["suggestedModel"] = v
			}
			if v, ok := brief["trivial"].(bool); ok {
				taskBody["trivial"] = v
			}
			if v, ok := brief["dependsOn"].([]any); ok {
				taskBody["dependsOn"] = v
			}
		}
		stub := map[string]any{
			"stepId": stepID,
			"name":   "TASK",
			"taskId": taskID,
			"status": "PENDING",
			"applicability": map[string]any{
				"applicable": true,
			},
			"startedAt": now,
			"task":      taskBody,
		}
		steps = append(steps, stub)
		existingTask[taskID] = true
	}
	raw["steps"] = steps
}

// mergeTaskPayload partitions the agent's payload between the `task` (top)
// fields and the nested `task.execution` fields, using the CUE schema as
// the SSOT for which keys belong where:
//
//   - Keys present in `#TaskExecution`        → set on task.
//   - Keys present in `#TaskExecutionResult`  → set inside task.execution.
//   - Caller-supplied `execution: {...}` block is merged into task.execution
//     (deep, not replaced — fields the seed already set survive when not
//     overridden).
//
// Unknown keys are dropped (consistent with sanitize* helpers). The split
// lets agents author the natural flat shape `{scope, title, agents, gates, ...}`
// and have it routed correctly without manual nesting.
func mergeTaskPayload(task, payload map[string]any) {
	taskFields, errA := schema.AllowedFields("#TaskExecution")
	resultFields, errB := schema.AllowedFields("#TaskExecutionResult")
	if errA != nil || errB != nil {
		// Defensive fallback — preserve historical behaviour (assume
		// the entire payload is the execution result) so a stale embed
		// doesn't break callers.
		task["execution"] = payload
		return
	}

	exec, _ := task["execution"].(map[string]any)
	if exec == nil {
		exec = map[string]any{}
	}
	// Caller-provided `execution: {...}` is merged first.
	if explicit, ok := payload["execution"].(map[string]any); ok {
		maps.Copy(exec, explicit)
	}
	for k, v := range payload {
		if k == "execution" {
			continue
		}
		switch {
		case resultFields[k]:
			exec[k] = v
		case taskFields[k]:
			task[k] = v
		}
		// Unknown keys: drop silently.
	}
	task["execution"] = exec
}

// canonicalStepName resolves a user-facing phase name (possibly an alias
// like "TASKS" or "BRAINSTORM") to its canonical CUE step name.
//
// Delegates to schema.ResolveStepAlias — the SSOT for CLI alias resolution.
// All future alias additions belong in schema.StepTypeAliases (one file).
func canonicalStepName(phase string) string {
	return schema.ResolveStepAlias(phase)
}
