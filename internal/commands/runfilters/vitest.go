package runfilters

import (
	"bytes"
	"strings"
)

func init() {
	DefaultRegistry.Register("vitest", filterVitest)
	DefaultRegistry.Register("npx vitest", filterVitest)
}

// filterVitest compresses vitest output by dropping passing test lines and
// blank lines between them, keeping failures, error messages, and summary.
func filterVitest(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))
	var out []byte

	for _, raw := range lines {
		line := string(raw)
		trimmed := strings.TrimSpace(line)

		// Always drop blank lines.
		if trimmed == "" {
			continue
		}

		// Drop passing test lines (✓ prefix after trimming) and skipped lines (↓).
		if strings.HasPrefix(trimmed, "✓") || strings.HasPrefix(trimmed, "↓") {
			continue
		}

		// Keep everything else: failures (×), FAIL file lines, summary lines,
		// error messages.
		out = append(out, raw...)
		out = append(out, '\n')
	}

	return out
}

// keepVitestLine reports whether a line should be retained when filtering.
// Used by tests to verify inclusion decisions.
func keepVitestLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "✓") || strings.HasPrefix(trimmed, "↓") {
		return false
	}
	return true
}
