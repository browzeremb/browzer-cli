package workflow

import (
	"strings"
	"testing"
)

// TestWriteAuditCompact_ShortPathUnchanged — when AuditLine.Path already fits
// under the cap, the compact form is byte-identical to WriteAudit and no
// elision marker appears.
func TestWriteAuditCompact_ShortPathUnchanged(t *testing.T) {
	line := AuditLine{
		Verb:        "patch",
		Path:        "/tmp/wf.json",
		Mode:        AuditModeDaemonAsync,
		WriteID:     "wf-1",
		StepID:      "STEP_01_PRD",
		ValidatedOk: true,
	}

	var compact, full strings.Builder
	WriteAuditCompact(&compact, line)
	WriteAudit(&full, line)

	if compact.String() != full.String() {
		t.Fatalf("under-cap path: compact and full should match\ncompact=%q\nfull=%q",
			compact.String(), full.String())
	}
	if strings.Contains(compact.String(), auditCompactEllipsisRune) {
		t.Fatalf("compact emitted ellipsis on under-cap path: %q", compact.String())
	}
}

// TestWriteAuditCompact_LongPathElided — when the path value would dominate,
// it is replaced with "…<tail>" while every diagnostic field
// (validatedOk= / durable= / reason= / etc.) survives intact so existing
// stderr-grepping tests keep working.
func TestWriteAuditCompact_LongPathElided(t *testing.T) {
	longPath := "/var/folders/7w/ylqs_b455pq_pykwvp1nx4cw0000gn/T/TestStaleHandshake_FallsBackToStandalone858894/001/workflow.json"
	line := AuditLine{
		Verb:        "set-status",
		Path:        longPath,
		Mode:        AuditModeFallbackSync,
		WriteID:     "wf-abcdef0123",
		StepID:      "STEP_03_TASK_AGENT",
		ValidatedOk: true,
		Durable:     false,
		ElapsedMs:   42,
		Reason:      "daemon_unreachable",
	}

	var compact strings.Builder
	WriteAuditCompact(&compact, line)
	out := compact.String()

	for _, tok := range []string{"verb=set-status ", " stepId=STEP_03_TASK_AGENT", " validatedOk=true", " durable=false", " elapsedMs=42", " reason=daemon_unreachable"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q to survive elision, got %q", tok, out)
		}
	}
	if !strings.Contains(out, auditCompactEllipsisRune) {
		t.Fatalf("expected elision marker in compact line, got %q", out)
	}
	if !strings.Contains(out, "workflow.json") {
		t.Fatalf("path basename should survive elision, got %q", out)
	}
	if line.Path != longPath {
		t.Fatalf("WriteAuditCompact mutated caller's AuditLine.Path: got %q", line.Path)
	}
}

// TestShortenAuditPath_FallbackToBasename — when the cap is too small for
// even the marker + a meaningful suffix, fall back to "…/<basename>".
func TestShortenAuditPath_FallbackToBasename(t *testing.T) {
	got := shortenAuditPath("/a/b/c/d.json", 2)
	if got != "…/d.json" {
		t.Fatalf("fallback basename: got %q, want %q", got, "…/d.json")
	}
}
