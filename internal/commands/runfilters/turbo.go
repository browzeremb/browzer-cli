package runfilters

import (
	"bytes"
	"strings"
)

func init() {
	DefaultRegistry.Register("pnpm turbo", filterTurbo)
	DefaultRegistry.Register("turbo", filterTurbo)
}

// filterTurbo compresses turbo output by dropping per-package success lines
// and verbose headers for passing packages, keeping failures and the final
// summary.
func filterTurbo(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))
	var out []byte

	for _, raw := range lines {
		line := string(raw)
		trimmed := strings.TrimSpace(line)

		// Always drop blank lines.
		if trimmed == "" {
			continue
		}

		// Drop success lines (✓ package#task).
		if strings.HasPrefix(trimmed, "✓") {
			continue
		}

		// Drop "• Packages in scope" verbose header lines.
		if strings.HasPrefix(trimmed, "•") {
			continue
		}

		// Keep everything else: ✗ failure lines, error output, Tasks summary,
		// FAIL/ERROR lines from nested test output, >>> task headers.
		out = append(out, raw...)
		out = append(out, '\n')
	}

	return out
}
