package runfilters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	DefaultRegistry.Register("go test", filterGoTest)
}

// goTestEvent is the NDJSON event emitted by `go test -json`.
type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// filterGoTest compresses `go test` output (standard or NDJSON) by dropping
// passing lines and keeping only failures and error context.
func filterGoTest(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))

	// Detect NDJSON mode: first non-empty line starts with '{'.
	for _, raw := range lines {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "{") {
			return filterGoTestNDJSON(lines)
		}
		break
	}

	return filterGoTestStandard(lines)
}

// filterGoTestStandard handles plain `go test` output.
func filterGoTestStandard(lines [][]byte) []byte {
	var out []byte
	passCount := 0
	failCount := 0

	for _, raw := range lines {
		line := string(raw)
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}

		// Drop individual passing test lines.
		if strings.HasPrefix(trimmed, "--- PASS:") {
			passCount++
			continue
		}
		// Count "ok" package lines but don't emit them — we'll emit a summary.
		if strings.HasPrefix(trimmed, "ok ") || strings.HasPrefix(trimmed, "ok\t") {
			passCount++
			continue
		}
		// Drop "[no test files]" informational lines.
		if strings.HasPrefix(trimmed, "?") && strings.Contains(trimmed, "[no test files]") {
			continue
		}
		// Count FAIL package lines.
		if strings.HasPrefix(trimmed, "FAIL") {
			failCount++
		}

		// Keep FAIL lines, error output, and everything else.
		out = append(out, raw...)
		out = append(out, '\n')
	}

	// When there are no failures, emit a single summary instead of nothing.
	if failCount == 0 && len(out) == 0 {
		return []byte(fmt.Sprintf("ok (%d pass)\n", passCount))
	}
	return out
}

// testKey uniquely identifies a test within a package for buffering.
type testKey struct {
	pkg  string
	test string
}

// filterGoTestNDJSON handles `go test -json` NDJSON output.
// It buffers all output events per test and retroactively emits them only
// for tests whose final action was "fail" — so panic bodies, cmp.Diff output,
// and custom t.Errorf payloads are preserved even when they lack FAIL/Error
// keyword tokens.
func filterGoTestNDJSON(lines [][]byte) []byte {
	// Per-test output accumulator. Keyed by (package, test-name).
	// Package-level events use test="" in the key.
	outputBuf := map[testKey][]string{}

	// failedKeys tracks tests that received a "fail" action, in order.
	failedKeys := []testKey{}
	failedSet := map[testKey]bool{}

	var out []byte

	for _, raw := range lines {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			continue
		}

		var ev goTestEvent
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
			// Non-JSON line — pass through verbatim.
			out = append(out, raw...)
			out = append(out, '\n')
			continue
		}

		key := testKey{pkg: ev.Package, test: ev.Test}

		switch ev.Action {
		case "output":
			// Accumulate all output for later; emit eagerly only for package-level
			// non-test output that looks like a build failure (no Test field).
			outputBuf[key] = append(outputBuf[key], ev.Output)

		case "fail":
			// Record failure order if not already seen.
			if !failedSet[key] {
				failedSet[key] = true
				failedKeys = append(failedKeys, key)
			}

		// Drop "pass", "run", "skip", "pause", "cont", "bench" actions.
		default:
		}
	}

	// All tests passed — emit a single summary line.
	if len(failedKeys) == 0 {
		passCount := 0
		for _, lines := range outputBuf {
			_ = lines
			passCount++
		}
		return []byte(fmt.Sprintf("ok (%d pass)\n", passCount))
	}

	// Emit all output for failed tests in the order failures were observed.
	for _, key := range failedKeys {
		// Emit compact header.
		name := key.test
		if name == "" {
			name = key.pkg
		}
		out = append(out, []byte(fmt.Sprintf("FAIL %s (%s)\n", name, key.pkg))...)

		// Emit buffered output lines for this test so the full failure body
		// (panic, cmp.Diff, t.Errorf payload) is visible.
		for _, line := range outputBuf[key] {
			out = append(out, []byte(line)...)
			if !strings.HasSuffix(line, "\n") {
				out = append(out, '\n')
			}
		}
	}

	return out
}
