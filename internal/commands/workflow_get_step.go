package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/diagnostic"
	"github.com/browzeremb/browzer-cli/internal/session"
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
	var fieldFlag string
	var invariantsFlag string
	var exitOnly bool

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

--field a,b,c  Project only the listed top-level fields from the step body.
               Missing fields exit non-zero with "unknown field: <name>" +
               a did-you-mean suggestion.

--invariants   Controls CLAUDE.md invariants block emission per session:
                 auto (default) — emit full block on first call; hash
                   marker only on subsequent calls with same content.
                 full — always emit the full block.
                 hash — always emit the hash marker only.
                 skip — suppress the invariants block entirely.

--exit-only    Suppress stdout AND stderr on error so callers can probe step
               existence without noisy output:
                 if browzer get-step CODE_REVIEW --id <feat> --exit-only; then
                   # step exists — consume it normally
                 fi

Exit codes: 0 success, 2 step not found / phase unknown / unknown field.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			phase := args[0]

			// failGetStep normalises the two error shapes at all three early-exit
			// sites: when --exit-only is set it returns a silent exit-2 sentinel
			// (cliExitErrSilent — no message printed, no stderr); otherwise it
			// propagates the original error so callers see the full diagnostic.
			// Keeping the divergence in one closure makes it easier to change
			// the exit code (e.g. to 4 for not-found) without touching three
			// independent guard blocks.
			failGetStep := func(err error) error {
				if exitOnly {
					return cliExitErrSilent(2)
				}
				return err
			}

			path, err := resolveWorkflowPathForGetSave(cmd, idFlag, topLevel)
			if err != nil {
				return failGetStep(err)
			}

			_, raw, err := loadWorkflow(path)
			if err != nil {
				return failGetStep(err)
			}

			view, err := wfview.Build(raw, phase)
			if err != nil {
				// Self-heal: if no step exists yet but a staging file is
				// present (autosave hook never fired — e.g. operator wrote
				// via Bash heredoc, or in another session), persist it on
				// the fly and re-build the view. Skips virtual phases and
				// TASK_NN (TASK steps must be seeded by tasks-manifest).
				if _, healed := tryHealFromStaging(cmd, path, phase); healed {
					_, raw2, err2 := loadWorkflow(path)
					if err2 == nil {
						view, err = wfview.Build(raw2, phase)
					} else {
						err = err2
					}
				}
				if err != nil {
					return failGetStep(cliExitErr(2, err))
				}
			}

			// FR-1: --field projection.
			if fieldFlag != "" {
				return runGetStepFieldProjection(cmd, view, fieldFlag, jsonMode)
			}

			// FR-5: invariants hashing for TASK_NN phases (which carry Invariants).
			invMode := session.ParseInvariantsMode(invariantsFlag)
			if len(view.Invariants) > 0 {
				applyInvariantsHashToView(&view, invMode)
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
	cmd.Flags().StringVar(&fieldFlag, "field", "", "comma-separated list of top-level fields to project from the step body")
	cmd.Flags().StringVar(&invariantsFlag, "invariants", "auto", "invariants block emission: auto|full|hash|skip")
	cmd.Flags().BoolVar(&exitOnly, "exit-only", false, "suppress stdout/stderr on error; exit code is the only signal (0=found, 2=not found/phase unknown)")
	if topLevel {
		_ = cmd.MarkFlagRequired("id")
	}
	return cmd
}

// runGetStepFieldProjection handles --field projection for get-step.
// It projects the requested fields from view.Body (or the full StepView JSON
// for fields not in Body). Missing fields produce exit 2 with a did-you-mean.
func runGetStepFieldProjection(cmd *cobra.Command, view wfview.StepView, fieldFlag string, jsonMode bool) error {
	// Marshal the full view to a flat map for uniform field access.
	viewBytes, err := json.Marshal(view)
	if err != nil {
		return fmt.Errorf("marshal view for projection: %w", err)
	}
	var viewMap map[string]any
	if err := json.Unmarshal(viewBytes, &viewMap); err != nil {
		return fmt.Errorf("unmarshal view for projection: %w", err)
	}

	// Also expose Body fields at the top level for convenience — this lets
	// `--field executionStrategy` work for CONFIG without requiring `--field body.executionStrategy`.
	// Body fields are merged at the same level but only for lookup (not for shadowing StepView fields).
	lookupMap := make(map[string]any, len(viewMap))
	for k, v := range viewMap {
		lookupMap[k] = v
	}
	if body, ok := viewMap["body"].(map[string]any); ok {
		for k, v := range body {
			if _, exists := lookupMap[k]; !exists {
				lookupMap[k] = v
			}
		}
	}

	// Build pool of all known field names for did-you-mean.
	pool := make([]string, 0, len(lookupMap))
	for k := range lookupMap {
		pool = append(pool, k)
	}

	fields := parseFieldList(fieldFlag)
	projected := make(map[string]any, len(fields))
	for _, f := range fields {
		v, ok := lookupMap[f]
		if !ok {
			// Did-you-mean suggestion.
			suggestions := diagnostic.DidYouMean(f, pool)
			hint := ""
			if len(suggestions) > 0 {
				hint = "; " + diagnostic.FormatSuggestion(suggestions)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "unknown field: %s%s\n", f, hint)
			return cliExitErr(2, fmt.Errorf("unknown field: %s", f))
		}
		projected[f] = v
	}

	if jsonMode {
		// Emit projected JSON.
		b, err := json.MarshalIndent(projected, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal projected fields: %w", err)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return err
	}

	// Plain text: emit each field as "field: value".
	out := cmd.OutOrStdout()
	for _, f := range fields {
		v := projected[f]
		switch sv := v.(type) {
		case string:
			_, _ = fmt.Fprintf(out, "%s: %s\n", f, sv)
		default:
			b, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(out, "%s: %s\n", f, string(b))
		}
	}
	return nil
}

// parseFieldList splits a comma-separated field list, trimming spaces and
// ignoring empty entries.
func parseFieldList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// applyInvariantsHashToView applies the per-session invariants hash logic.
// When ShouldEmitFull returns false, the full Invariants slice is replaced
// with a single-element slice containing the hash marker, so the downstream
// template renders it as a one-liner.
func applyInvariantsHashToView(v *wfview.StepView, mode session.InvariantsMode) {
	if len(v.Invariants) == 0 {
		return
	}
	sid := session.SessionID()
	block := strings.Join(v.Invariants, "\n")
	hash := session.HashInvariants(block)

	emitFull := session.ShouldEmitFull(sid, hash, mode)
	// Always record the current hash regardless of which path we take.
	// This ensures the suppress path also refreshes the cache so the next
	// call compares against the current hash, not a stale prior value.
	session.RecordHash(sid, hash)

	if emitFull {
		// Full block — leave v.Invariants unchanged.
		return
	}

	// Replace the full block with the hash marker.
	v.Invariants = []string{session.HashMarker(hash)}
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
