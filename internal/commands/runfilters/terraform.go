package runfilters

import (
	"bytes"
	"regexp"
)

func init() {
	DefaultRegistry.Register("terraform", filterTerraform)
}

// filterTerraform compresses terraform plan output.
// Typical plan output is 50 KB+; this filter targets >90 % byte reduction.
//
// Output structure:
//  1. The "Plan: N to add, M to change, K to destroy." summary line, reformatted.
//  2. At most 5 resource change lines (lines starting with +, -, or ~).
//
// All other content (resource attribute diffs, refresh output, notes, etc.) is dropped.
//
// Fallback contract: when no "Plan: ..." summary line is found (e.g. terraform
// apply output, init output, or future format changes), the raw stdout is
// returned unchanged so no signal is lost. TrackRun is still called by the run
// command with source "browzer-run-terraform" and savings=0, making
// zero-compression runs visible in the token-economy ledger.
func filterTerraform(stdout []byte) []byte {
	// Match "Plan: N to add, M to change, K to destroy."
	planSummaryRe := regexp.MustCompile(`Plan:\s*(\d+)\s+to add,\s*(\d+)\s+to change,\s*(\d+)\s+to destroy`)

	lines := bytes.Split(stdout, []byte("\n"))

	var summaryLine []byte
	var resourceLines [][]byte

	for _, raw := range lines {
		trimmed := bytes.TrimSpace(raw)

		// Capture plan summary.
		if summaryLine == nil {
			if m := planSummaryRe.FindSubmatch(trimmed); m != nil {
				summaryLine = []byte("Plan: +" + string(m[1]) + " add  ~" + string(m[2]) + " change  -" + string(m[3]) + " destroy")
				continue
			}
		}

		// Collect resource change lines: + / - / ~ prefix (not part of attribute diffs).
		// Exclude lines that are deeply indented attribute diffs (contain " = " or leading spaces).
		if len(trimmed) > 0 {
			first := trimmed[0]
			if (first == '+' || first == '-' || first == '~') && len(resourceLines) < 5 {
				// Skip lines that look like attribute diffs (contain " = " or "#" comments).
				if !bytes.Contains(trimmed, []byte(" = ")) && !bytes.HasPrefix(trimmed, []byte("# ")) {
					resourceLines = append(resourceLines, trimmed)
				}
			}
		}
	}

	// If no summary found, the output is not a terraform plan; return raw.
	if summaryLine == nil {
		return stdout
	}

	var out []byte
	out = append(out, summaryLine...)
	out = append(out, '\n')
	for _, rl := range resourceLines {
		out = append(out, rl...)
		out = append(out, '\n')
	}
	return out
}
