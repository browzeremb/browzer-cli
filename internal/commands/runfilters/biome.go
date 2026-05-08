package runfilters

import (
	"bytes"
	"regexp"
	"strings"
)

func init() {
	DefaultRegistry.Register("biome", filterBiome)
}

// reBiomeFileLine matches biome diagnostic location lines:
// packages/foo/src/bar.ts:10:5 lint/suspicious/noExplicitAny ━━━━━━━
// The pattern matches any file extension with a :LINE:COL suffix so that
// newer biome targets (.svelte, .vue, .astro, .css, .html, .md) are handled
// without requiring an allowlist update.
var reBiomeFileLine = regexp.MustCompile(`^[^\s:]+\.[a-zA-Z0-9]+:\d+:\d+`)

// reBiomeSummary matches the "Checked N files" summary line.
var reBiomeSummary = regexp.MustCompile(`^Checked \d+ file`)

// filterBiome compresses biome check/lint output by retaining only:
//   - File:line:col diagnostic lines
//   - Diagnostic message lines (those starting with ✖)
//   - The summary line ("Checked N files …")
//
// Dropped: decorative ━━━ separator lines, blank lines, ℹ info lines.
func filterBiome(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))
	var out []byte

	for _, raw := range lines {
		line := string(raw)
		trimmed := strings.TrimSpace(line)

		// Drop blank lines.
		if trimmed == "" {
			continue
		}

		// Keep summary line (before any other checks).
		if rebiomeSummaryLine(trimmed) {
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		// Keep file:line:col diagnostic location lines (strip trailing ━ decoration).
		// Check this before the general ━ drop so location lines are not discarded.
		if rebiomeFileLocationLine(trimmed) {
			clean := trimTrailingDecoration(trimmed)
			out = append(out, []byte(clean)...)
			out = append(out, '\n')
			continue
		}

		// Drop decorative separator lines (━━━ …) — must come after file line check.
		if strings.ContainsRune(trimmed, '━') {
			continue
		}

		// Drop ℹ info/hint lines.
		if strings.HasPrefix(trimmed, "ℹ") {
			continue
		}

		// Keep ✖ error/warning message lines.
		if strings.HasPrefix(trimmed, "✖") {
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		// Drop everything else (context lines, blank padding, etc.).
	}

	return out
}

func rebiomeSummaryLine(trimmed string) bool {
	return reBiomeSummary.MatchString(trimmed)
}

func rebiomeFileLocationLine(trimmed string) bool {
	return reBiomeFileLine.MatchString(trimmed)
}

// trimTrailingDecoration strips trailing ━ characters and surrounding spaces.
func trimTrailingDecoration(s string) string {
	idx := strings.Index(s, " ━")
	if idx != -1 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}
