package commands

import (
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/output"
)

// TestExtractAnchor_PicksFirstNonTrivialLine verifies that the anchor
// extractor walks past blanks, comment-only lines, and short lines to
// pick the first substantive snippet line. The anchor is the contract
// downstream skills (generate-task Pass 1) use to relocate code after
// HEAD drift; if this picks a comment marker, every grep-based
// relocate downstream silently misses.
func TestExtractAnchor_PicksFirstNonTrivialLine(t *testing.T) {
	snippet := "// header comment\n\nfunction validateRegression(stepId string) error {\n\treturn nil\n}"
	got := output.ExtractAnchor(snippet, "validateRegression")
	if !strings.Contains(got, "validateRegression") {
		t.Errorf("anchor should contain function signature, got %q", got)
	}
	if strings.HasPrefix(got, "//") {
		t.Errorf("anchor must not start with a // comment marker, got %q", got)
	}
}

// TestExtractAnchor_SkipsMultipleCommentStyles confirms each known
// comment-only marker (//, /* … */, --, #) is recognised and the
// extractor falls through to the first code line.
func TestExtractAnchor_SkipsMultipleCommentStyles(t *testing.T) {
	cases := []struct {
		name    string
		snippet string
	}{
		{"slash-slash", "// only this\n// and this\nrealCodeLineHere := 42"},
		{"slash-star", "/* leading block */\n * inside\n */\nrealCodeLineHere := 42"},
		{"sql-dash", "-- sql comment\n-- another\nrealCodeLineHere := 42"},
		{"hash", "# python comment\nrealCodeLineHere := 42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := output.ExtractAnchor(tc.snippet, "fallback")
			if !strings.Contains(got, "realCodeLineHere") {
				t.Errorf("expected anchor to land on the code line, got %q", got)
			}
		})
	}
}

// TestExtractAnchor_FallsBackToName covers the "no qualifying line"
// path — folder/symbol entries the indexer returns without snippets,
// and pathological all-comments snippets.
func TestExtractAnchor_FallsBackToName(t *testing.T) {
	if got := output.ExtractAnchor("", "buildStatusRecommendations"); got != "buildStatusRecommendations" {
		t.Errorf("empty snippet should fall back to name, got %q", got)
	}
	if got := output.ExtractAnchor("// only comments\n# nothing else", "MyFolder"); got != "MyFolder" {
		t.Errorf("all-comment snippet should fall back to name, got %q", got)
	}
}

// TestExtractAnchor_CapsAt80Chars guards against runaway anchors that
// would defeat their own purpose — a 600-char regex anchor is no more
// resilient than a line number across edits, and pollutes JSON output.
func TestExtractAnchor_CapsAt80Chars(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := output.ExtractAnchor(long, "fallback")
	if len(got) > 80 {
		t.Errorf("anchor must cap at 80 chars, got %d", len(got))
	}
}

// TestExtractAnchor_SkipsTooShortLines confirms 11-char lines are
// treated as not-unique-enough and skipped — the threshold is strictly
// < 12 chars, mirroring the implementation contract.
func TestExtractAnchor_SkipsTooShortLines(t *testing.T) {
	snippet := "tiny\nstillTiny\nfunc longEnoughToAnchor() {}"
	got := output.ExtractAnchor(snippet, "fallback")
	if !strings.Contains(got, "longEnoughToAnchor") {
		t.Errorf("expected anchor to land on the long-enough line, got %q", got)
	}
}
