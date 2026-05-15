package hooks

// Exit codes for the browzer→shell-wrapper protocol, NOT Claude Code's
// native hook exit-code contract. These codes are an internal convention
// between the Go subcommand (browzer hook <name>) and the thin shell wrapper
// (packages/skills/hooks/<name>.sh) that delegates to it. The shell wrapper
// MUST remain in the call path; it translates these codes into the form
// Claude Code actually understands:
//
//   Claude Code's native contract (from its hook docs):
//     exit 0 — success; stdout JSON is parsed for hookSpecificOutput
//     exit 2 — blocking error; stdout is IGNORED, stderr fed back to Claude
//     any other exit — non-blocking error; transcript shows a hook-error notice
//
//   Browzer internal codes (shell wrapper translates these):
//     0 (ExitAllow)       — allow; wrapper forwards stdout payload and exits 0
//     1 (ExitPassthrough) — no decision; wrapper exits 0 with no payload
//     2 (ExitDeny)        — deny; wrapper emits a decision JSON and exits 2
//     3 (ExitAsk)         — ask user; wrapper emits a decision JSON and exits 0
//
// WARNING: If browzer hook <name> is invoked by Claude Code DIRECTLY (without
// the shell wrapper), ExitPassthrough (1) surfaces as a non-blocking hook error
// and ExitAsk (3) has no effect — Claude Code has no native "ask" exit code.
const (
	ExitAllow       = 0
	ExitPassthrough = 1
	ExitDeny        = 2
	ExitAsk         = 3
)
