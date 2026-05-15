package hooks

import (
	"os"
	"strings"
)

// GateAllowed reports whether the named hook is currently allowed to run.
// Returns false (disabled) when either:
//
//   - BROWZER_HOOK env is set to "off", "0", or "false" (case-insensitive),
//     which silences every hook regardless of the granular CSV; OR
//   - hookName appears in the comma-separated BROWZER_HOOK_DISABLE list
//     (whitespace-tolerant, case-insensitive). Empty entries are dropped.
//
// hookName is the kebab-case identifier — typically the cobra subcommand
// name (e.g. "rewrite-bash", "session-start") matching the JS guard
// filename minus the `browzer-` prefix. An empty hookName skips the
// granular CSV check but still honours the binary BROWZER_HOOK kill-switch.
//
// Subcommands consuming this helper should exit ExitPassthrough (1) when
// GateAllowed returns false — that signals "no decision; Claude Code
// keeps its default behaviour" rather than blocking the tool call.
func GateAllowed(hookName string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BROWZER_HOOK"))) {
	case "off", "0", "false":
		return false
	}

	if hookName == "" {
		return true
	}

	raw := os.Getenv("BROWZER_HOOK_DISABLE")
	if raw == "" {
		return true
	}
	wanted := strings.ToLower(strings.TrimSpace(hookName))
	for entry := range strings.SplitSeq(raw, ",") {
		trimmed := strings.ToLower(strings.TrimSpace(entry))
		if trimmed == "" {
			continue
		}
		if trimmed == wanted {
			return false
		}
	}
	return true
}
