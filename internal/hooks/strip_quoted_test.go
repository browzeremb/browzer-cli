package hooks

import (
	"strings"
	"testing"
)

func TestStripQuoted_EmptyInput(t *testing.T) {
	if got := StripQuoted(""); got != "" {
		t.Fatalf("expected empty output for empty input, got %q", got)
	}
}

func TestStripQuoted_PlainCommand(t *testing.T) {
	in := "ls -la /tmp"
	if got := StripQuoted(in); got != in {
		t.Fatalf("plain command should pass through unchanged: got %q, want %q", got, in)
	}
}

func TestStripQuoted_SingleQuotedRemoved(t *testing.T) {
	got := StripQuoted("echo 'hidden TOKEN here' done")
	if strings.Contains(got, "TOKEN") {
		t.Fatalf("single-quoted body should be stripped, got %q", got)
	}
	if !strings.Contains(got, "echo") || !strings.Contains(got, "done") {
		t.Fatalf("skeleton tokens lost: %q", got)
	}
}

func TestStripQuoted_DoubleQuotedRemoved(t *testing.T) {
	got := StripQuoted(`grep "browzer explore SECRET" file.txt`)
	if strings.Contains(got, "SECRET") {
		t.Fatalf("double-quoted body should be stripped, got %q", got)
	}
	if !strings.Contains(got, "grep") || !strings.Contains(got, "file.txt") {
		t.Fatalf("skeleton tokens lost: %q", got)
	}
}

func TestStripQuoted_DoubleQuotedEscape(t *testing.T) {
	// The escaped quote should NOT close the string early.
	got := StripQuoted(`echo "a\"TOKEN\"b" tail`)
	if strings.Contains(got, "TOKEN") {
		t.Fatalf("escaped quote should not leak inner body, got %q", got)
	}
	if !strings.Contains(got, "tail") {
		t.Fatalf("expected tail in skeleton, got %q", got)
	}
}

func TestStripQuoted_CommandSubstitution(t *testing.T) {
	got := StripQuoted(`echo $(browzer explore SECRET --json) done`)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "explore") {
		t.Fatalf("$() body should be stripped, got %q", got)
	}
	if !strings.Contains(got, "echo") || !strings.Contains(got, "done") {
		t.Fatalf("skeleton tokens lost: %q", got)
	}
}

func TestStripQuoted_HeredocPlain(t *testing.T) {
	in := "cat <<EOF\nbrowzer explore SECRET\nEOF\ndone"
	got := StripQuoted(in)
	if strings.Contains(got, "SECRET") {
		t.Fatalf("heredoc body should be stripped, got %q", got)
	}
	if !strings.Contains(got, "done") {
		t.Fatalf("post-heredoc skeleton lost: %q", got)
	}
}

func TestStripQuoted_HeredocQuotedDelim(t *testing.T) {
	in := "cat <<'EOF'\nbrowzer explore SECRET\nEOF\ntail"
	got := StripQuoted(in)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "browzer explore") {
		t.Fatalf("quoted-delim heredoc body should be stripped, got %q", got)
	}
	if !strings.Contains(got, "tail") {
		t.Fatalf("skeleton post-heredoc lost: %q", got)
	}
}

func TestStripQuoted_HeredocDashDelim(t *testing.T) {
	in := "cat <<-EOF\n\t\tbrowzer explore SECRET\n\tEOF\nafter"
	got := StripQuoted(in)
	if strings.Contains(got, "SECRET") {
		t.Fatalf("<<- heredoc body should be stripped, got %q", got)
	}
	if !strings.Contains(got, "after") {
		t.Fatalf("skeleton post-heredoc lost: %q", got)
	}
}

// Required invariant test: a heredoc opened INSIDE $(...) must not surface
// tokens from its body when consumers grep the skeleton.
func TestStripQuoted_HeredocInsideCommandSubstitution(t *testing.T) {
	in := "git commit -m \"$(cat <<'EOF'\nbrowzer explore \"TOKEN\"\nEOF\n)\""
	got := StripQuoted(in)
	if strings.Contains(got, "TOKEN") {
		t.Fatalf("TOKEN must not surface from heredoc-inside-$() : got %q", got)
	}
	if strings.Contains(got, "browzer explore") {
		t.Fatalf("browzer explore must not surface from heredoc-inside-$() : got %q", got)
	}
	if !strings.Contains(got, "git commit") {
		t.Fatalf("outer skeleton lost: %q", got)
	}
}

func TestStripQuoted_AnsiCQuoted(t *testing.T) {
	got := StripQuoted(`printf $'hidden\nTOKEN' tail`)
	if strings.Contains(got, "TOKEN") {
		t.Fatalf("$'...' body should be stripped, got %q", got)
	}
	if !strings.Contains(got, "tail") {
		t.Fatalf("skeleton lost: %q", got)
	}
}

func TestStripQuoted_UnterminatedSingleQuote(t *testing.T) {
	got := StripQuoted("echo 'unterminated TOKEN")
	if strings.Contains(got, "TOKEN") {
		t.Fatalf("unterminated single-quote should swallow remainder, got %q", got)
	}
	if !strings.Contains(got, "echo") {
		t.Fatalf("pre-quote skeleton lost: %q", got)
	}
}

func TestStripQuoted_UnterminatedDoubleQuote(t *testing.T) {
	got := StripQuoted(`echo "unterminated TOKEN`)
	if strings.Contains(got, "TOKEN") {
		t.Fatalf("unterminated double-quote should swallow remainder, got %q", got)
	}
}

func TestStripQuoted_NestedDollarParen(t *testing.T) {
	in := "echo $(foo $(bar SECRET)) tail"
	got := StripQuoted(in)
	if strings.Contains(got, "SECRET") {
		t.Fatalf("nested $() depth tracking failed, got %q", got)
	}
	if !strings.Contains(got, "tail") {
		t.Fatalf("post-substitution skeleton lost: %q", got)
	}
}
