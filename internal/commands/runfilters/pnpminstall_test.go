package runfilters

import (
	"strings"
	"testing"
)

// largePnpmOutput returns a synthetic pnpm install output of the given size
// that contains a recognisable summary line.
func largePnpmOutput(targetBytes int, summaryLine string) []byte {
	// Build padding lines that fill the target size.
	padding := strings.Repeat("progress | resolved 500, reused 498, downloaded 2, added 500\n", targetBytes/60+1)
	// Ensure the summary and elapsed lines are present at the start.
	maxPad := max(targetBytes-len(summaryLine)-15, 0)
	return []byte(summaryLine + "\nDone in 3.1s\n" + padding[:maxPad])
}

// TestFilterPnpmInstall_ByteReduction asserts >85 % byte reduction on ≥5 KB pnpm output.
func TestFilterPnpmInstall_ByteReduction(t *testing.T) {
	const targetRaw = 10 * 1024 // 10 KB
	summaryLine := "Packages: +42 -3 ~7"
	raw := largePnpmOutput(targetRaw, summaryLine)

	filtered := filterPnpmInstall(raw)

	ratio := float64(len(filtered)) / float64(len(raw))
	if ratio > 0.15 {
		t.Errorf("byte reduction insufficient: filtered=%d raw=%d ratio=%.1f%% (want ≤15%%)",
			len(filtered), len(raw), ratio*100)
	}
}

// TestFilterPnpmInstall_OutputFormat verifies the compact summary format.
func TestFilterPnpmInstall_OutputFormat(t *testing.T) {
	input := []byte("Packages: +42 -3 ~7\nDone in 3.1s\nsome other junk\n")
	got := string(filterPnpmInstall(input))

	if !strings.Contains(got, "added 42 packages") {
		t.Errorf("expected 'added 42 packages' in output, got: %q", got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "(3.1s)") {
		t.Errorf("expected elapsed time in output, got: %q", got)
	}
}

// TestFilterPnpmInstall_NpmStyle verifies npm-style per-line patterns.
func TestFilterPnpmInstall_NpmStyle(t *testing.T) {
	input := []byte("added 150 packages from 80 contributors in 12.5s\n" +
		strings.Repeat("npm WARN ...\n", 200))
	got := string(filterPnpmInstall(input))

	if !strings.Contains(got, "added 150 packages") {
		t.Errorf("expected 'added 150 packages' in output, got: %q", got)
	}
}

// TestFilterPnpmInstall_Fallback verifies raw output is returned when no patterns match.
func TestFilterPnpmInstall_Fallback(t *testing.T) {
	input := []byte("some unexpected output that does not match any pattern\n")
	got := filterPnpmInstall(input)
	if string(got) != string(input) {
		t.Errorf("expected fallback to raw output, got: %q", got)
	}
}

// TestFilterPnpmInstall_RegisteredInDefaultRegistry verifies init() registration.
func TestFilterPnpmInstall_RegisteredInDefaultRegistry(t *testing.T) {
	for _, args := range [][]string{
		{"pnpm", "install"},
		{"npm", "install"},
	} {
		if fn := DefaultRegistry.Resolve(args); fn == nil {
			t.Errorf("expected filter registered for %v", args)
		}
	}
}
