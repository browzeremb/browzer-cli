package walker

import "strings"

// RerootGitignore prefixes every pattern in a nested .gitignore so it
// applies relative to the *root* ignore matcher. Without this,
// patterns like `node_modules` in `apps/web/.gitignore` would be checked
// against the absolute repo root and silently miss every nested match.
//
// Comments and blank lines are preserved as-is. Negations (`!pattern`)
// are re-rooted while keeping the `!` prefix.
//
// Mirrors src/lib/walker.ts:rerootGitignore byte-for-byte.
func RerootGitignore(text, relDir string) string {
	if relDir == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			lines[i] = line
			continue
		}
		negated := strings.HasPrefix(trimmed, "!")
		body := trimmed
		if negated {
			body = trimmed[1:]
		}
		stripped := strings.TrimPrefix(body, "/")
		rooted := relDir + "/" + stripped
		if negated {
			rooted = "!" + rooted
		}
		lines[i] = rooted
	}
	return strings.Join(lines, "\n")
}
