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
	var lockTimeout time.Duration
	var argPairs []string
	var argJSONPairs []string

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

Example:

  browzer workflow patch --workflow "$WORKFLOW" \
    --arg id="$STEP_ID" \
    --argjson changes="$CHANGES_JSON_ARRAY" \
    --jq '(.steps[] | select(.stepId == $id)).task.scope = $changes'`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jqExpr == "" {
				return fmt.Errorf("--jq is required; provide a jq mutation expression")
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
			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}
			dispatchErr := dispatchToDaemonOrFallback(cmd, wfPath, "patch", wf.MutatorArgs{
				JQExpr: jqExpr,
				JQVars: vars,
			}, mode, noLock, lockTimeout)
			if dispatchErr != nil && strings.Contains(dispatchErr.Error(), "undefined variable") {
				fmt.Fprintln(os.Stderr, "hint: this may indicate a stale daemon binary that does not understand --arg/--argjson; try `browzer daemon stop && browzer daemon start`")
			}
			return dispatchErr
		},
	}

	cmd.Flags().StringVar(&jqExpr, "jq", "", "jq expression to apply as mutation (required)")
	cmd.Flags().StringArrayVar(&argPairs, "arg", nil, "bind $KEY to literal string VALUE in the jq expression: --arg KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&argJSONPairs, "argjson", nil, "bind $KEY to parsed JSON value in the jq expression: --argjson KEY=<json> (repeatable)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	parent.AddCommand(cmd)
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
