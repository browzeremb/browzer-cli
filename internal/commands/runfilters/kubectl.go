package runfilters

import (
	"bytes"
	"strconv"
	"strings"
)

func init() {
	DefaultRegistry.Register("kubectl", filterKubectl)
}

// filterKubectl compresses kubectl get tabular output.
// Typical kubectl get pods output is 2–10 KB; target is >65 % byte reduction.
//
// Logic:
//  1. Detect the header row by the presence of "NAME" as the first or second
//     token (second when "NAMESPACE" precedes it in `kubectl get pods -A`
//     all-namespaces output).
//  2. Keep the header row verbatim.
//  3. Drop data rows where the STATUS column is "Running" or "Completed".
//  4. Keep all other rows (Pending, Error, CrashLoopBackOff, etc.).
//  5. Append a "N/M pods running" summary line.
//  6. Fall back to raw output if no header row is detected.
func filterKubectl(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))
	// Remove trailing empty element from split.
	for len(lines) > 0 && len(bytes.TrimSpace(lines[len(lines)-1])) == 0 {
		lines = lines[:len(lines)-1]
	}

	// Find the header row index.
	// Matches both standard output (fields[0]=="NAME") and all-namespaces output
	// via `kubectl get pods -A` (fields[0]=="NAMESPACE", fields[1]=="NAME").
	headerIdx := -1
	for i, l := range lines {
		fields := strings.Fields(string(l))
		if len(fields) > 0 && (fields[0] == "NAME" || (fields[0] == "NAMESPACE" && len(fields) > 1 && fields[1] == "NAME")) {
			headerIdx = i
			break
		}
	}

	// No header detected — fall back.
	if headerIdx < 0 {
		return stdout
	}

	headerLine := lines[headerIdx]

	// Determine column index of STATUS by splitting the header.
	headerFields := strings.Fields(string(headerLine))
	statusCol := -1
	for i, f := range headerFields {
		if f == "STATUS" {
			statusCol = i
			break
		}
	}

	var kept [][]byte
	kept = append(kept, headerLine)

	totalPods := 0
	runningPods := 0

	for _, l := range lines[headerIdx+1:] {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		totalPods++
		fields := strings.Fields(string(l))
		status := ""
		if statusCol >= 0 && statusCol < len(fields) {
			status = fields[statusCol]
		}
		if status == "Running" || status == "Completed" {
			runningPods++
			continue // drop healthy rows
		}
		kept = append(kept, l)
	}

	var out []byte
	for _, l := range kept {
		out = append(out, l...)
		out = append(out, '\n')
	}
	// Append summary.
	summary := bytes.Join([][]byte{
		[]byte(""),
		[]byte(formatPodSummary(runningPods, totalPods)),
	}, []byte("\n"))
	out = append(out, summary...)
	out = append(out, '\n')
	return out
}

// formatPodSummary builds the count summary string.
func formatPodSummary(running, total int) string {
	return strings.Join([]string{
		strconv.Itoa(running) + "/" + strconv.Itoa(total) + " pods running",
	}, "")
}
