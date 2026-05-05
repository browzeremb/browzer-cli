package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

func registerWorkflowPatch(parent *cobra.Command) {
	var jqExpr string
	var bulkJSON string
	var lockTimeout time.Duration
	var argPairs []string
	var argJSONPairs []string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "patch",
		Short: "Apply a jq mutation expression to workflow.json",
		Long: `Apply a jq mutation expression to workflow.json.

Bind variables can be supplied via --arg and --argjson, mirroring the jq CLI
(but using KEY=VALUE form because cobra cannot consume two positional values
per flag):

  --arg KEY=VALUE          bind $KEY to the literal string VALUE
  --argjson KEY=<json>     bind $KEY to the parsed JSON value

Both flags may be repeated. KEY must match [A-Za-z_][A-Za-z0-9_]*.

Bulk mode (A3.3, 2026-05-05):

  --bulk '<json-array>'    apply N jq expressions sequentially under ONE
                           advisory-lock window. The argument is a JSON
                           array of strings; each entry is run against the
                           in-memory result of the previous one. CUE
                           validation runs ONCE at the end. Mutually
                           exclusive with --jq.

Dry-run mode (A1.4, 2026-05-05):

  --dry-run                applies the mutation, validates the post-
                           mutation document against the CUE schema,
                           and prints {ok, errors, beforeHash,
                           afterHash, diffPaths, stepId} to stdout
                           WITHOUT writing the file. Combinable with
                           --bulk.

Example:

  browzer workflow patch --workflow "$WORKFLOW" \
    --arg id="$STEP_ID" \
    --argjson changes="$CHANGES_JSON_ARRAY" \
    --jq '(.steps[] | select(.stepId == $id)).task.scope = $changes'

  browzer workflow patch --bulk '[".foo = 1", ".bar = 2"]' --dry-run`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jqExpr == "" && bulkJSON == "" {
				return fmt.Errorf("--jq or --bulk is required; provide a jq mutation expression")
			}
			if jqExpr != "" && bulkJSON != "" {
				return fmt.Errorf("--jq and --bulk are mutually exclusive")
			}
			vars, err := parseJQBindFlags(argPairs, argJSONPairs)
			if err != nil {
				return err
			}
			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}
			noLock, _ := cmd.Flags().GetBool("no-lock")
			if !noLock {
				noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
			}

			// Resolve the effective single jq program to feed the
			// mutator. For --bulk we coalesce N expressions into one
			// pipeline that's applied atomically under a single lock.
			effectiveExpr, err := resolvePatchExpression(jqExpr, bulkJSON)
			if err != nil {
				return err
			}

			// Dry-run path: never touches the file. Skips the dispatch
			// machinery entirely (no lock, no daemon round-trip) so the
			// preview is cheap.
			if dryRun {
				return runPatchDryRun(cmd, wfPath, effectiveExpr, vars)
			}

			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}
			dispatchErr := dispatchToDaemonOrFallback(cmd, wfPath, "patch", wf.MutatorArgs{
				JQExpr: effectiveExpr,
				JQVars: vars,
			}, mode, noLock, lockTimeout)
			if dispatchErr != nil && strings.Contains(dispatchErr.Error(), "undefined variable") &&
				!quietByDefaultUnderLLM(cmd) {
				// A2 (2026-05-05): hint suppressed under BROWZER_LLM=1 so
				// LLM tool-result contexts stay clean. The error message
				// itself still surfaces — only the soft-warning hint
				// disappears.
				fmt.Fprintln(os.Stderr, "hint: this may indicate a stale daemon binary that does not understand --arg/--argjson; try `browzer daemon stop && browzer daemon start`")
			}
			return dispatchErr
		},
	}

	cmd.Flags().StringVar(&jqExpr, "jq", "", "jq expression to apply as mutation (required unless --bulk)")
	cmd.Flags().StringVar(&bulkJSON, "bulk", "", "JSON array of jq expressions applied sequentially under one lock")
	cmd.Flags().StringArrayVar(&argPairs, "arg", nil, "bind $KEY to literal string VALUE in the jq expression: --arg KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&argJSONPairs, "argjson", nil, "bind $KEY to parsed JSON value in the jq expression: --argjson KEY=<json> (repeatable)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate mutation without writing workflow.json; emit JSON preview to stdout")
	parent.AddCommand(cmd)
}

// resolvePatchExpression collapses --jq / --bulk into a single jq
// expression. --bulk is a JSON array of strings; the strings are
// concatenated with `|` so the post-CUE-validation step sees the
// composed pipeline result. Returns an error if --bulk is malformed
// or empty.
//
// Empty strings inside --bulk are tolerated and skipped (callers
// generating bulk arrays from templates may leave gaps); a fully
// empty array (after skipping) returns an explicit error so the
// caller doesn't get a no-op silently.
func resolvePatchExpression(jqExpr, bulkJSON string) (string, error) {
	if bulkJSON == "" {
		return jqExpr, nil
	}
	var exprs []string
	if err := json.Unmarshal([]byte(bulkJSON), &exprs); err != nil {
		return "", fmt.Errorf("--bulk: invalid JSON array: %w", err)
	}
	cleaned := make([]string, 0, len(exprs))
	for _, e := range exprs {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		cleaned = append(cleaned, "("+e+")")
	}
	if len(cleaned) == 0 {
		return "", fmt.Errorf("--bulk: array contains no non-empty expressions")
	}
	return strings.Join(cleaned, " | "), nil
}

// runPatchDryRun applies the jq expression in-memory, validates the
// post-mutation document against the CUE schema, and writes a single
// JSON object to stdout describing the outcome. Returns a non-nil
// error only on infrastructure failures (file IO, marshal); CUE
// validation failures land in `result.Errors` and exit with status 1
// so shells can branch on success.
func runPatchDryRun(cmd *cobra.Command, wfPath, expr string, vars map[string]any) error {
	res, err := wf.ApplyDryRun(wfPath, "patch", wf.MutatorArgs{
		JQExpr: expr,
		JQVars: vars,
	})
	if err != nil {
		return err
	}
	out, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("dry-run: marshal result: %w", err)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
	if !res.Ok {
		// Non-zero exit so shell `&&` chains stop on validation
		// failure. The result is already on stdout — the error
		// itself stays terse.
		return fmt.Errorf("dry-run: %d validation error(s)", len(res.Errors))
	}
	return nil
}

// parseJQBindFlags merges --arg and --argjson pairs into a single
// jq-bind-variable map. Errors on duplicate keys (across both flag sets) so
// callers can't silently override a binding.
//
// --arg KEY= (empty value) is valid and binds $KEY to the empty string "".
// --argjson KEY= (empty value) is rejected with a targeted error because an
// empty string is not valid JSON; use --arg KEY= instead.
func parseJQBindFlags(argPairs, argJSONPairs []string) (map[string]any, error) {
	if len(argPairs) == 0 && len(argJSONPairs) == 0 {
		return nil, nil
	}
	vars := make(map[string]any, len(argPairs)+len(argJSONPairs))
	// origin tracks which flag set originally bound each key so that the
	// duplicate-key error can name the offending flag.
	origin := make(map[string]string, len(argPairs)+len(argJSONPairs))
	for _, pair := range argPairs {
		key, val, ok := splitBindPair(pair)
		if !ok {
			return nil, fmt.Errorf("--arg %q: expected KEY=VALUE form", pair)
		}
		if orig, dup := origin[key]; dup {
			return nil, fmt.Errorf("--arg %q: variable %q already bound by %s", pair, key, orig)
		}
		vars[key] = val
		origin[key] = "--arg"
	}
	for _, pair := range argJSONPairs {
		key, raw, ok := splitBindPair(pair)
		if !ok {
			return nil, fmt.Errorf("--argjson %q: expected KEY=<json> form", pair)
		}
		if raw == "" {
			return nil, fmt.Errorf("--argjson %q: empty JSON value (did you mean --arg %s=?)", pair, key)
		}
		if orig, dup := origin[key]; dup {
			return nil, fmt.Errorf("--argjson %q: variable %q already bound by %s", pair, key, orig)
		}
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, fmt.Errorf("--argjson %q: invalid JSON for %q: %w", pair, key, err)
		}
		vars[key] = decoded
		origin[key] = "--argjson"
	}
	return vars, nil
}

// splitBindPair splits "KEY=VALUE" on the first '=' so VALUE itself may
// contain '=' characters (common for base64 / JSON literals). Returns
// (key, value, ok) where ok is false if no '=' is present or KEY is empty.
func splitBindPair(pair string) (string, string, bool) {
	idx := strings.IndexByte(pair, '=')
	if idx <= 0 {
		return "", "", false
	}
	return pair[:idx], pair[idx+1:], true
}
