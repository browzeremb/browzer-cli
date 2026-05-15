package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// contractHasShellOperatorRE matches shell metacharacters that introduce
// compound commands. Single-segment browzer commands may be validated;
// compound commands are passed through to avoid false positives.
// Recognised operators (in precedence order): logical-AND (&&), logical-OR
// (||), bare pipe (|), semicolon (;), newline (\n).
var contractHasShellOperatorRE = regexp.MustCompile(`&&|\|\||\||;|\n`)

// contractSubVerbRE matches the browzer sub-verb in a segment.
var contractSubVerbRE = regexp.MustCompile(`^browzer\s+(explore|search|deps)\b`)

// contractHasContractFlagRE matches the presence of --save, --json, or --schema.
var contractHasContractFlagRE = regexp.MustCompile(`\s--(save|json|schema)\b`)

// contractGitFirstRE matches commands whose effective first token is `git`.
var contractGitFirstRE = regexp.MustCompile(`^\s*(?:[A-Z_][A-Z0-9_]*=\S*\s+)*git(\s|$)`)

// contractInput is the PreToolUse(Bash) hook envelope for the contract guard.
type contractInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
}

// contractEnvelope is the PreToolUse output envelope.
type contractEnvelope struct {
	HookSpecificOutput *contractSpecific `json:"hookSpecificOutput,omitempty"`
}

type contractSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// newContractCommand returns the cobra command for the contract guard.
func newContractCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "contract",
		Short: "Enforce plugin hook contracts",
		RunE:  runContract,
	}
}

// runContract is the cobra RunE for the contract guard.
func runContract(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook contract: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, out := contractDecide(raw)
	if out != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
	os.Exit(code)
	return nil // unreachable
}

// contractDecide is the testable core. Returns exit code and optional envelope.
func contractDecide(raw []byte) (int, *contractEnvelope) {
	var in contractInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough, nil
	}

	cmd := in.ToolInput.Command
	if cmd == "" {
		return internalhooks.ExitPassthrough, nil
	}

	// Skip standalone (non-compound) git invocations — they pass through.
	if contractGitFirstRE.MatchString(cmd) && !contractHasShellOperatorRE.MatchString(cmd) {
		return internalhooks.ExitPassthrough, nil
	}

	// Strip quoted regions so we don't false-match inside heredoc/string bodies.
	skeleton := internalhooks.StripQuoted(cmd)

	// Look for a browzer explore|search|deps segment.
	segments := contractHasShellOperatorRE.Split(skeleton, -1)
	var matchedSub string
	for _, seg := range segments {
		m := contractSubVerbRE.FindStringSubmatch(strings.TrimSpace(seg))
		if m != nil {
			matchedSub = m[1]
			break
		}
	}
	if matchedSub == "" {
		return internalhooks.ExitPassthrough, nil
	}

	// If the skeleton already carries --save / --json / --schema, pass through.
	if contractHasContractFlagRE.MatchString(skeleton) {
		return internalhooks.ExitPassthrough, nil
	}

	message := fmt.Sprintf(
		"`browzer %s` was invoked without `--save`, `--json`, or `--schema`. "+
			"Human-formatted output is hard to parse in an agent loop. "+
			"Re-run with `--save /tmp/%s.json` (preferred) or `--json`. "+
			"Inspect the response shape first with `browzer %s --schema`.",
		matchedSub, matchedSub, matchedSub,
	)

	strict := isStrictMode()
	if strict {
		out := &contractEnvelope{
			HookSpecificOutput: &contractSpecific{
				HookEventName:            "PreToolUse",
				PermissionDecision:       "deny",
				PermissionDecisionReason: "Blocked (BROWZER_STRICT=1): " + message,
				AdditionalContext:        "Blocked (BROWZER_STRICT=1): " + message,
			},
		}
		return internalhooks.ExitDeny, out
	}

	// Non-strict: first offense → ask; repeat → allow.
	seen := contractOffenseSeen(in.SessionID, matchedSub)
	decision := "ask"
	if seen {
		decision = "allow"
	}

	out := &contractEnvelope{
		HookSpecificOutput: &contractSpecific{
			HookEventName:            "PreToolUse",
			PermissionDecision:       decision,
			PermissionDecisionReason: message,
			AdditionalContext:        message,
		},
	}
	return internalhooks.ExitAllow, out
}

// isStrictMode returns true when BROWZER_STRICT is set to "1", "true",
// "yes", or "on" (case-insensitive).
func isStrictMode() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("BROWZER_STRICT")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// contractOffenseSeen is a once-per-(sessionID, sub) offense ledger. First
// call for the pair returns false (caller emits `ask`); subsequent calls
// return true (caller downgrades to `allow`). State lives in a sentinel
// file `$TMPDIR/.browzer-guard/<sessionID>/contract-<sub>` created via
// `O_CREATE|O_EXCL|O_WRONLY` — the same atomic-create idiom
// session_sentinel.go uses for its banner dedup. Replaces the previous
// JSON read-modify-write cycle (4 syscalls per Bash PreToolUse) with a
// single mkdir + open per first call and a single open per repeat.
//
// Uses the package-shared tmpDirFn seam so tests can redirect the
// sentinel directory via WithTmpDir.
func contractOffenseSeen(sessionID, sub string) bool {
	if sessionID == "" {
		return false
	}
	dir := filepath.Join(tmpDirFn(), ".browzer-guard", sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	path := filepath.Join(dir, "contract-"+sub)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_ = f.Close()
		return false
	}
	if os.IsExist(err) {
		return true
	}
	return false
}
