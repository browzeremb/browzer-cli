package runfilters

import (
	"bytes"
	"encoding/json"
	"strings"
)

func init() {
	DefaultRegistry.Register("cargo test", filterCargoTest)
}

// cargoTestEvent is the NDJSON event from `cargo test --format json` (unstable).
type cargoTestEvent struct {
	Type    string `json:"type"`
	Event   string `json:"event"`
	Name    string `json:"name"`
	Stdout  string `json:"stdout"`
	Message string `json:"message"`
}

// filterCargoTest compresses `cargo test` output by keeping only failures and
// the final result line.
func filterCargoTest(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))

	// Detect NDJSON mode: first non-empty line starts with '{'.
	for _, raw := range lines {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "{") {
			return filterCargoTestNDJSON(lines)
		}
		break
	}

	return filterCargoTestStandard(lines)
}

// filterCargoTestStandard handles plain `cargo test` output.
func filterCargoTestStandard(lines [][]byte) []byte {
	var out []byte
	inFailures := false

	for _, raw := range lines {
		line := string(raw)
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			// Preserve blank lines inside the failures: section.
			if inFailures {
				out = append(out, raw...)
				out = append(out, '\n')
			}
			continue
		}

		// Enter failures: section.
		if trimmed == "failures:" {
			inFailures = true
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		if inFailures {
			// The failures section ends at the "test result:" line.
			if strings.HasPrefix(trimmed, "test result:") {
				inFailures = false
				out = append(out, raw...)
				out = append(out, '\n')
				continue
			}
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		// Keep FAILED test lines.
		if strings.HasSuffix(trimmed, "... FAILED") || strings.Contains(trimmed, "FAILED") {
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		// Keep final result lines (both ok and FAILED).
		if strings.HasPrefix(trimmed, "test result:") {
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		// Drop "running N tests" and "test ... ok" lines.
	}

	return out
}

// filterCargoTestNDJSON handles `cargo test --format json` NDJSON output.
func filterCargoTestNDJSON(lines [][]byte) []byte {
	var out []byte
	for _, raw := range lines {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			continue
		}

		var ev cargoTestEvent
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
			// Non-JSON line — pass through.
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		if ev.Type == "test" && ev.Event == "failed" {
			out = append(out, []byte("FAILED "+ev.Name+"\n")...)
			if ev.Stdout != "" {
				out = append(out, []byte(ev.Stdout)...)
				if !strings.HasSuffix(ev.Stdout, "\n") {
					out = append(out, '\n')
				}
			}
			if ev.Message != "" {
				out = append(out, []byte(ev.Message)...)
				if !strings.HasSuffix(ev.Message, "\n") {
					out = append(out, '\n')
				}
			}
		}
		// Drop ok, started, suite events.
	}
	return out
}
