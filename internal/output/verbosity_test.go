package output

import (
	"bytes"
	"strings"
	"testing"
)

// TestEmitStalenessWarningTo_RoutesToProvidedWriter mirrors the
// `--save --json` contract: callers pass os.Stderr so stdout (which
// holds the JSON payload bound for --save's file or the stdout pipe)
// stays clean. We assert the warning lands on the target writer
// verbatim.
//
// Coverage: T-AC-9 (RETRO §16 #5).
func TestEmitStalenessWarningTo_RoutesToProvidedWriter(t *testing.T) {
	var buf bytes.Buffer
	wrote := EmitStalenessWarningTo(&buf, false /*quiet*/, true /*stale*/, 3)
	if !wrote {
		t.Fatalf("expected warning to be written")
	}
	got := buf.String()
	if !strings.Contains(got, "Index 3 commits behind") {
		t.Errorf("warning missing expected text: %q", got)
	}
	if !strings.Contains(got, "browzer sync") {
		t.Errorf("warning missing remediation hint: %q", got)
	}
	// Trailing newline is part of the contract — agents grep for the
	// line and a missing \n breaks line-oriented parsers.
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("warning missing trailing newline: %q", got)
	}
}

// TestEmitStalenessWarningTo_QuietSuppresses asserts that --quiet wins
// even when the index is stale: nothing is written ANYWHERE (not even
// stderr).
//
// Coverage: T-AC-10 (RETRO §16 #5 quiet branch).
func TestEmitStalenessWarningTo_QuietSuppresses(t *testing.T) {
	var buf bytes.Buffer
	wrote := EmitStalenessWarningTo(&buf, true /*quiet*/, true /*stale*/, 7)
	if wrote {
		t.Fatalf("expected warning to be suppressed under --quiet")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output under --quiet, got %q", buf.String())
	}
}

// TestEmitStalenessWarningTo_FreshIsSilent guards the !stale path: if
// the index is up to date, no warning is written regardless of quiet.
// Catches a regression where a refactor flips the condition.
func TestEmitStalenessWarningTo_FreshIsSilent(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		var buf bytes.Buffer
		wrote := EmitStalenessWarningTo(&buf, quiet, false /*stale*/, 0)
		if wrote {
			t.Errorf("expected no warning when stale=false (quiet=%v)", quiet)
		}
		if buf.Len() != 0 {
			t.Errorf("expected empty output when stale=false (quiet=%v), got %q", quiet, buf.String())
		}
	}
}

// TestEmitStalenessWarningTo_NonQuietWritesMessage is the
// human-CLI-UX path (no --json, no --save): the warning surfaces on
// stderr so operators see "you're N commits behind" before results
// render.
//
// Coverage: AC criterion #3 — without --quiet and without --json, the
// warning still surfaces on stderr.
func TestEmitStalenessWarningTo_NonQuietWritesMessage(t *testing.T) {
	var buf bytes.Buffer
	wrote := EmitStalenessWarningTo(&buf, false, true, 1)
	if !wrote {
		t.Fatalf("expected warning to surface in non-quiet mode")
	}
	if !strings.HasPrefix(buf.String(), "⚠ Index 1 commits behind") {
		t.Errorf("unexpected warning prefix: %q", buf.String())
	}
}
