package runfilters

import (
	"strings"
	"testing"
)

func TestFilterBiome_NonStandardExtensions(t *testing.T) {
	// FR-06: biome supports .svelte, .vue, .astro, .css etc. — must match.
	for _, ext := range []string{"svelte", "vue", "astro", "css", "html", "md"} {
		input := "packages/foo/src/bar." + ext + ":5:1 lint/rule/name ━━━━━━━\n" +
			"  ✖ Some lint message.\n" +
			"Checked 1 file in 10ms. Found 1 error.\n"
		got := string(filterBiome([]byte(input)))
		if !strings.Contains(got, "packages/foo/src/bar."+ext+":5:1") {
			t.Errorf("expected file location for .%s extension to be retained, got: %q", ext, got)
		}
	}
}

func TestFilterBiome_AllClean(t *testing.T) {
	input := `Checked 42 files in 234ms. No errors found.
`
	got := string(filterBiome([]byte(input)))

	if !strings.Contains(got, "Checked 42 files") {
		t.Errorf("expected summary line in output, got: %q", got)
	}
}

func TestFilterBiome_WithErrors(t *testing.T) {
	input := `packages/foo/src/bar.ts:10:5 lint/suspicious/noExplicitAny ━━━━━━━
  ✖ Avoid using the ` + "`any`" + ` type.

packages/foo/src/bar.ts:15:3 lint/correctness/noUnusedVariables ━━
  ✖ This variable is unused.

Checked 42 files in 234ms. Found 2 errors.
`
	got := string(filterBiome([]byte(input)))

	// File location lines must be present (stripped of ━ decoration).
	if !strings.Contains(got, "packages/foo/src/bar.ts:10:5 lint/suspicious/noExplicitAny") {
		t.Errorf("expected first file:line line in output, got: %q", got)
	}
	if !strings.Contains(got, "packages/foo/src/bar.ts:15:3 lint/correctness/noUnusedVariables") {
		t.Errorf("expected second file:line line in output, got: %q", got)
	}

	// ✖ message lines must be present.
	if !strings.Contains(got, "✖ Avoid using the") {
		t.Errorf("expected first error message in output, got: %q", got)
	}
	if !strings.Contains(got, "✖ This variable is unused.") {
		t.Errorf("expected second error message in output, got: %q", got)
	}

	// Summary must be present.
	if !strings.Contains(got, "Checked 42 files") {
		t.Errorf("expected summary line in output, got: %q", got)
	}

	// ━━━ decorators must be dropped.
	if strings.Contains(got, "━") {
		t.Errorf("expected decorative ━ lines to be dropped, got: %q", got)
	}

	// Blank lines must be dropped.
	for _, line := range strings.Split(got, "\n") {
		if line == "" {
			// trailing newline is acceptable
			continue
		}
		if strings.TrimSpace(line) == "" {
			t.Errorf("unexpected blank line in output: %q", got)
		}
	}
}
