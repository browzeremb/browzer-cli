package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"strings"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// hookInput models the Claude Code PreToolUse(Bash) JSON envelope.
// Only the fields the rewrite-bash guard consumes are typed; unknown
// fields are tolerated and round-tripped via the raw map below so that
// updatedInput preserves any extra keys (matching the JS source's
// `{ ...input.tool_input, command: newCmd }` spread).
type hookInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	SessionID string         `json:"session_id"`
}

// hookSpecificOutput is the inner payload of the rewrite envelope. The
// permissionDecision is always "allow" on the rewrite path — the
// Claude Code hook is approving the rewritten command, not the
// original.
type hookSpecificOutput struct {
	HookEventName      string         `json:"hookEventName"`
	PermissionDecision string         `json:"permissionDecision"`
	UpdatedInput       map[string]any `json:"updatedInput,omitempty"`
	AdditionalContext  string         `json:"additionalContext,omitempty"`
}

// rewriteEnvelope is the outer JSON object written to stdout on the
// rewrite path. additionalContext lives at the top level for the
// run-proxy "pipe/redirect" suggestion branch (no rewrite, just hint);
// the rewrite path nests its additionalContext inside hookSpecificOutput
// to match the JS source.
type rewriteEnvelope struct {
	HookSpecificOutput *hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
	AdditionalContext  string              `json:"additionalContext,omitempty"`
}

// browzerCommandRE matches a command whose leading token is `browzer`.
var browzerCommandRE = regexp.MustCompile(`^\s*browzer(\s|$)`)

// browzerAlreadyHasEnvRE matches an existing BROWZER_LLM=… env prefix
// anywhere in the command line so we don't double-inject.
var browzerAlreadyHasEnvRE = regexp.MustCompile(`(^|\s)BROWZER_LLM=`)

// browzerAlreadyHasFlagRE matches the equivalent --llm or --llm=value
// flag so we don't redundantly add the env when the operator already
// chose the flag form.
var browzerAlreadyHasFlagRE = regexp.MustCompile(`(^|\s)--llm(\s|=|$)`)

// browzerWrappedSubshellRE matches commands that open with `(` or `{`
// (subshell or brace-group) — prepending env there breaks the shell
// parse, so the guard skips them.
var browzerWrappedSubshellRE = regexp.MustCompile(`^\s*[({]`)

// browzerRunPrefixRE matches commands already wrapped by `browzer run`
// so the run-proxy compression pass doesn't double-wrap.
var browzerRunPrefixRE = regexp.MustCompile(`^\s*browzer\s+run\b`)

// compoundShellRE matches any shell metacharacter that turns a single
// invocation into a compound command (pipe, redirect, semicolon, chain).
var compoundShellRE = regexp.MustCompile(`[|;&<>]`)

// chainOpRE matches the strict chain-operator subset of compoundShellRE
// (`;` and `&`). When skeleton contains only `|<>` (no chain ops), the
// guard emits a manual-rewrite hint; chain-op compounds are silently
// skipped to avoid suggesting a misleading `browzer run` wrapper.
var chainOpRE = regexp.MustCompile(`[;&]`)

// catHeadTailRE matches the cat/head/tail/less/more fallback shape:
// exactly one verb followed by exactly one single-token path with no
// pipes, redirects, chains, or flags. The verbose token class
// `[^\s|;&<>]+` rejects flags (since `-` is allowed but the path can
// never legitimately START with `-` after the verb; the JS source
// matches with the same class and relies on ClassifyPath / size gates
// to reject pathological inputs).
var catHeadTailRE = regexp.MustCompile(`^\s*(cat|head|tail|less|more)\s+([^\s|;&<>]+)\s*$`)

// runRewritePattern pairs a regex with a label used only in tests/diag.
type runRewritePattern struct {
	re    *regexp.Regexp
	label string
}

// runRewritePatterns mirrors the JS source's RUN_REWRITE_PATTERNS list.
// Patterns wrap to `browzer run <cmd>` so the CLI can apply output
// compression, token-economy filters, and structured result formatting.
// Order matters: `pnpm turbo` MUST come before any generic pnpm pattern
// so it is not shadowed.
var runRewritePatterns = []runRewritePattern{
	// git subcommands worth compressing.
	{re: regexp.MustCompile(`^\s*(git\s+(status|log|diff|push|pull|add|commit))\b`), label: "git"},
	// vitest (standalone or via npx).
	{re: regexp.MustCompile(`^\s*(npx\s+)?vitest\b`), label: "vitest"},
	// pnpm turbo (must come before generic pnpm).
	{re: regexp.MustCompile(`^\s*pnpm\s+turbo\b`), label: "pnpm-turbo"},
	// go test.
	{re: regexp.MustCompile(`^\s*go\s+test\b`), label: "go-test"},
	// cargo test.
	{re: regexp.MustCompile(`^\s*cargo\s+test\b`), label: "cargo-test"},
	// biome check/lint/format (standalone, npx, or pnpm exec).
	{re: regexp.MustCompile(`^\s*(npx\s+|pnpm\s+exec\s+)?biome\s+(check|lint|format)\b`), label: "biome"},
	// tsc (standalone, npx, or pnpm exec).
	{re: regexp.MustCompile(`^\s*(npx\s+|pnpm\s+exec\s+)?tsc\b`), label: "tsc"},
	// pnpm run / pnpm exec vitest (not turbo — turbo handled above).
	{re: regexp.MustCompile(`^\s*pnpm\s+(run\s+|exec\s+|--filter\s+\S+\s+)?vitest\b`), label: "vitest"},
}

// llmBannerText is the additionalContext surfaced on the FIRST
// BROWZER_LLM=1 injection within a session. Subsequent injections in
// the same session emit silently — the agent already learned the
// pattern.
const llmBannerText = "Browzer prefixed BROWZER_LLM=1 to suppress per-mutation audit telemetry and correlate workflow traces (override: BROWZER_LLM=0 or --llm=0)."

// newRewriteBashCommand returns the real `browzer hook rewrite-bash`
// cobra command, replacing the generic stub registered in hooks.go.
// The RunE writes either a rewrite envelope (exit 0) or returns without
// output and exits via internalhooks.ExitPassthrough on every skip
// branch.
func newRewriteBashCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rewrite-bash",
		Short: "Rewrite bash commands before execution",
		RunE:  runRewriteBash,
	}
}

// runRewriteBash is the cobra RunE. Exit-code protocol:
//
//   - exit 0 (ExitAllow)       — write rewrite envelope, command accepted.
//   - exit 1 (ExitPassthrough) — no decision; Claude Code keeps default.
//
// The function delegates to rewriteBashDecide for the testable core and
// uses os.Exit at the terminal boundary because cobra would otherwise
// print errors to stderr and apply its own exit-code mapping.
func runRewriteBash(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook rewrite-bash: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code := rewriteBashDecide(raw, cmd.OutOrStdout(), realEnv{})
	os.Exit(code)
	return nil // unreachable
}

// envLookup abstracts os.Getenv so tests can drive the BROWZER_LLM
// branch deterministically without poking the process environment.
type envLookup interface {
	Get(key string) string
}

type realEnv struct{}

func (realEnv) Get(key string) string { return os.Getenv(key) }

// rewriteBashDecide is the testable core of the guard. It returns the
// exit code the cobra RunE should propagate and writes the rewrite
// envelope (or hint envelope) to `out` on the rewrite branches. The
// function consults internalhooks.GateAllowed / IsInBrowzerWorkspace /
// SessionBannerEmittedOnce / ClassifyPath / NeverRewriteRE just like
// the production path; tests inject sentinels via $HOME + $TMPDIR.
//
// Orchestration: the function calls three named sub-deciders in priority
// order and returns the first non-passthrough result. Each sub-decider
// returns (envelope, exitCode, handled). When handled is true the
// sub-decider has produced a definitive decision; when false the
// orchestrator moves to the next sub-decider.
func rewriteBashDecide(raw []byte, out io.Writer, env envLookup) int {
	// Top-level gates — order matches the JS source.
	if !internalhooks.GateAllowed("rewrite-bash") {
		return internalhooks.ExitPassthrough
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return internalhooks.ExitPassthrough
	}

	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		// Malformed JSON: passthrough rather than deny so the Bash
		// call proceeds. The harness will surface the parse failure
		// in its own logs.
		return internalhooks.ExitPassthrough
	}
	if in.ToolName != "Bash" {
		return internalhooks.ExitPassthrough
	}
	cmdStr, ok := in.ToolInput["command"].(string)
	if !ok {
		return internalhooks.ExitPassthrough
	}

	// Sub-decider 1: BROWZER_LLM=1 injection on `browzer …` invocations.
	if env2, code, handled := decideBrowzerEnvInject(&in, cmdStr, env); handled {
		if env2 != nil {
			writeEnvelope(out, *env2)
		}
		return code
	}

	// Sub-decider 2: run-proxy compression for git/test/CLI tools.
	if env2, code, handled := decideRunProxy(&in, cmdStr); handled {
		if env2 != nil {
			writeEnvelope(out, *env2)
		}
		return code
	}

	// Sub-decider 3: cat/head/tail → browzer read fallback.
	if env2, code, handled := decideNeverRewrite(&in, cmdStr); handled {
		if env2 != nil {
			writeEnvelope(out, *env2)
		}
		return code
	}

	return internalhooks.ExitPassthrough
}

// decideBrowzerEnvInject handles the BROWZER_LLM=1 injection path.
//
// Every `browzer ...` invocation gets BROWZER_LLM=1 prefixed so the
// per-mutation audit line is suppressed in agent shells. Done as a
// hook (instead of inline `export BROWZER_LLM=1` in skill bash)
// because each Bash tool call in an agent harness runs in an isolated
// shell — `export` in one call does NOT persist to the next. The
// flag-equivalent --llm and per-call env are the only modes that
// actually work in agent context.
//
// Idempotence guards skip the prefix when:
//   - operator already set BROWZER_LLM=<anything> on this command,
//   - operator already passed --llm or --llm=<value>,
//   - the command starts with a subshell `(` or brace-group `{`
//     (we can't safely prepend env there without breaking shell parsing),
//   - the leading token isn't `browzer` (compound `cmd && browzer …`
//     — regex won't match; opt-out is implicit).
//
// Returns handled=true for any command whose leading token is `browzer`
// (whether the injection fires or is an idempotent skip). Returns
// handled=false for all other commands so the orchestrator falls through
// to the next sub-decider.
func decideBrowzerEnvInject(in *hookInput, cmdStr string, env envLookup) (*rewriteEnvelope, int, bool) {
	if !browzerCommandRE.MatchString(cmdStr) {
		return nil, internalhooks.ExitPassthrough, false
	}

	alreadyHasEnv := browzerAlreadyHasEnvRE.MatchString(cmdStr)
	alreadyHasFlag := browzerAlreadyHasFlagRE.MatchString(cmdStr)
	wrappedSubshell := browzerWrappedSubshellRE.MatchString(cmdStr)
	if alreadyHasEnv || alreadyHasFlag || wrappedSubshell {
		// Leading `browzer` but opted out — exit clean; do not fall
		// through to the cat/head/tail rewrite (it can't match a
		// browzer command anyway).
		return nil, internalhooks.ExitAllow, true
	}

	newCmd := "BROWZER_LLM=1 " + strings.TrimLeft(cmdStr, " \t")

	// Env-based guard: suppress banner when BROWZER_LLM is already
	// truthy in the incoming environment. Treat "0" and "false" as
	// falsy (shell convention), not just the empty string.
	rawEnv := env.Get("BROWZER_LLM")
	envAlreadySet := len(rawEnv) > 0 && rawEnv != "0" && rawEnv != "false"

	// File-sentinel once-per-session dedup via the shared helper.
	// SessionBannerEmittedOnce returns true on FIRST emission (banner
	// SHOULD fire) and false on subsequent emissions (banner SHOULD be
	// suppressed). On contended writes the helper errs toward emission
	// — banner-twice is acceptable; banner-never is not.
	sentinelFirstEmit := internalhooks.SessionBannerEmittedOnce(".browzer-llm-banner")
	bannerSuppressed := envAlreadySet || !sentinelFirstEmit

	updated := cloneInputWith(in.ToolInput, newCmd)
	envOut := rewriteEnvelope{
		HookSpecificOutput: &hookSpecificOutput{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			UpdatedInput:       updated,
		},
	}
	if !bannerSuppressed {
		envOut.HookSpecificOutput.AdditionalContext = llmBannerText
	}
	return &envOut, internalhooks.ExitAllow, true
}

// decideRunProxy handles run-proxy compression rewrites.
//
// Rewrites common shell tool invocations to `browzer run <cmd>` so the
// CLI can apply output compression, token-economy filters, and
// structured result formatting. Only simple (non-compound) commands are
// rewritten; pipes, redirects, and chain operators are left untouched.
//
// Returns handled=true when a pattern matches the command skeleton
// (whether the rewrite fires, a hint envelope is emitted, or a silent
// skip is performed). Returns handled=false when no pattern matches,
// passing control to the next sub-decider.
func decideRunProxy(in *hookInput, cmdStr string) (*rewriteEnvelope, int, bool) {
	if browzerRunPrefixRE.MatchString(cmdStr) {
		return nil, internalhooks.ExitPassthrough, false
	}

	skeleton := internalhooks.StripQuoted(cmdStr)
	for _, p := range runRewritePatterns {
		if !p.re.MatchString(skeleton) {
			continue
		}

		// Skip compound commands — pipes, redirects, semicolons,
		// chain operators.
		if compoundShellRE.MatchString(skeleton) {
			// For simple pipe/redirect cases (no chain operators like
			// && / || / ;), emit a one-line additionalContext so the
			// model knows why compression was bypassed. Chain-operator
			// compounds are silently skipped to avoid suggesting a
			// misleading `browzer run` wrapper.
			if !chainOpRE.MatchString(skeleton) {
				trimmed := strings.TrimSpace(cmdStr)
				hint := fmt.Sprintf(
					"[browzer] Skipped run-proxy rewrite for `%s` (compound command with pipe/redirect). To compress output manually: `browzer run %s`.",
					trimmed, trimmed,
				)
				envOut := rewriteEnvelope{AdditionalContext: hint}
				return &envOut, internalhooks.ExitAllow, true
			}
			// Chain-op compound — silent skip.
			return nil, internalhooks.ExitPassthrough, true
		}

		// Skip if the whole command matches the never-rewrite list
		// (e.g. config files referenced by path).
		if internalhooks.NeverRewriteRE.MatchString(cmdStr) {
			break
		}

		trimmed := strings.TrimSpace(cmdStr)
		newCmd := "browzer run " + trimmed
		// First emission in this session carries additionalContext;
		// subsequent emissions are silent.
		firstEmit := internalhooks.SessionBannerEmittedOnce(".browzer-runproxy-banner")
		updated := cloneInputWith(in.ToolInput, newCmd)
		envOut := rewriteEnvelope{
			HookSpecificOutput: &hookSpecificOutput{
				HookEventName:      "PreToolUse",
				PermissionDecision: "allow",
				UpdatedInput:       updated,
			},
		}
		if firstEmit {
			envOut.HookSpecificOutput.AdditionalContext = fmt.Sprintf(
				"Browzer rewrote `%s` → `%s` (run-proxy compression). Subsequent rewrites this session emit silently — the agent already learned the pattern.",
				trimmed, newCmd,
			)
		}
		return &envOut, internalhooks.ExitAllow, true
	}

	return nil, internalhooks.ExitPassthrough, false
}

// decideNeverRewrite handles the cat/head/tail → browzer read fallback
// and acts as the early-passthrough gate for paths that must never be
// rewritten.
//
// Matches exactly: <verb> <single-token-path>; rejects pipes, redirects,
// chains, and flags. Also enforces the NeverRewriteRE and ClassifyPath
// guards so only source-code files above the 40 KB size threshold are
// rewritten to `browzer read --filter=auto`.
//
// Returns handled=true when the cat/head/tail regex matches (whether or
// not the rewrite ultimately fires — size/classify/never-rewrite guards
// may still produce handled=true with ExitPassthrough). Returns
// handled=false when the regex does not match.
func decideNeverRewrite(in *hookInput, cmdStr string) (*rewriteEnvelope, int, bool) {
	m := catHeadTailRE.FindStringSubmatch(cmdStr)
	if m == nil {
		return nil, internalhooks.ExitPassthrough, false
	}

	filePath := m[2]
	if internalhooks.ClassifyPath(filePath) != internalhooks.ClassCode {
		return nil, internalhooks.ExitPassthrough, true
	}
	if internalhooks.NeverRewriteRE.MatchString(filePath) {
		return nil, internalhooks.ExitPassthrough, true
	}

	// Skip files small enough that the rewrite round-trip costs more
	// than it saves. 500 lines is roughly 2-3k tokens — below this the
	// daemon adds overhead with negligible savings, and the rewritten
	// output would force the model to re-parse "Browzer optimized..."
	// prose for raw content it could read directly.
	stat, err := os.Stat(filePath)
	if err != nil || !stat.Mode().IsRegular() {
		return nil, internalhooks.ExitPassthrough, true
	}
	// Cheap heuristic: average line length ~80 bytes; skip if <40KB.
	if stat.Size() < 40*1024 {
		return nil, internalhooks.ExitPassthrough, true
	}

	quoted, _ := json.Marshal(filePath)
	newCmd := "browzer read " + string(quoted) + " --filter=auto"
	trimmed := strings.TrimSpace(cmdStr)
	updated := cloneInputWith(in.ToolInput, newCmd)
	envOut := rewriteEnvelope{
		HookSpecificOutput: &hookSpecificOutput{
			HookEventName:      "PreToolUse",
			PermissionDecision: "allow",
			UpdatedInput:       updated,
			AdditionalContext: fmt.Sprintf(
				"Browzer rewrote `%s` → `%s` (token-economy filter applied).",
				trimmed, newCmd,
			),
		},
	}
	return &envOut, internalhooks.ExitAllow, true
}

// cloneInputWith returns a shallow copy of toolInput with the
// `command` key overwritten. Mirrors the JS source's
// `{ ...input.tool_input, command: newCmd }` spread so any extra keys
// the harness sent (description, etc.) survive the rewrite.
func cloneInputWith(toolInput map[string]any, newCmd string) map[string]any {
	out := make(map[string]any, len(toolInput)+1)
	maps.Copy(out, toolInput)
	out["command"] = newCmd
	return out
}

// writeEnvelope serialises the rewrite envelope to the supplied writer.
// On marshal failure the function emits nothing and lets the caller
// exit ExitPassthrough — better to skip a rewrite than to deny a
// command we can't describe.
func writeEnvelope(w io.Writer, env rewriteEnvelope) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	// json.Encoder appends a trailing newline; Claude Code tolerates
	// it. Matches the JS source's `process.stdout.write(JSON.stringify(…))`
	// closely enough — the harness reads to EOF.
	_ = enc.Encode(env)
}
