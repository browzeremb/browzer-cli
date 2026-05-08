package runfilters

import (
	"bytes"
	"regexp"
	"strings"
)

func init() {
	DefaultRegistry.Register("tsc", filterTsc)
	DefaultRegistry.Register("npx tsc", filterTsc)
}

// reTscErrorLine matches tsc non-pretty error lines:
// src/foo.ts(10,5): error TS2304: Cannot find name 'foo'.
var reTscErrorLine = regexp.MustCompile(`^[^\s(]+\(\d+,\d+\): (?:error|warning) TS\d+:`)

// reTscPrettyHeader matches the file header line emitted in --pretty mode:
// src/foo.ts:10:5 - error TS2304: Cannot find name 'foo'.
var reTscPrettyHeader = regexp.MustCompile(`^[^\s:]+:\d+:\d+ - (?:error|warning) TS\d+:`)

// reTscSummary matches the "Found N errors." summary line.
var reTscSummary = regexp.MustCompile(`^Found \d+ error`)

// filterTsc compresses TypeScript compiler output by retaining only:
//   - Error lines (both plain and --pretty header format)
//   - The summary line ("Found N errors.")
//
// Dropped: blank lines, info messages, context source lines, ~~~ / ^~~~ underlines.
//
// --pretty mode detection: output contains "~~~" underline sequences.
func filterTsc(stdout []byte) []byte {
	isPretty := bytes.Contains(stdout, []byte("~~~"))

	lines := bytes.Split(stdout, []byte("\n"))
	var out []byte

	for _, raw := range lines {
		line := string(raw)
		trimmed := strings.TrimSpace(line)

		// Drop blank lines.
		if trimmed == "" {
			continue
		}

		// Keep summary line.
		if reTscSummary.MatchString(trimmed) {
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		if isPretty {
			// Pretty mode: keep file:line:col header lines only.
			if reTscPrettyHeader.MatchString(trimmed) {
				out = append(out, raw...)
				out = append(out, '\n')
			}
			// Drop context lines and ~~~ / ^~~~ underlines.
			continue
		}

		// Non-pretty mode: keep error/warning lines.
		if reTscErrorLine.MatchString(trimmed) {
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		// Drop everything else (info messages, etc.).
	}

	return out
}
