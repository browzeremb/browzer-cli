package daemon

import "testing"

func TestApplyFilter_None_Passthrough(t *testing.T) {
	in := "function foo() {\n  return 42;\n}\n"
	out, lvl := ApplyFilter([]byte(in), "none", "foo.ts", ManifestFile{Language: "typescript"})
	if string(out) != in {
		t.Fatalf("none should passthrough; got %q", string(out))
	}
	if lvl != "none" {
		t.Fatalf("level = %q, want none", lvl)
	}
}

func TestApplyFilter_Minimal_StripsComments(t *testing.T) {
	in := "// header\nfunction foo() {\n  /* block */\n  return 42; // tail\n}\n"
	out, lvl := ApplyFilter([]byte(in), "minimal", "foo.ts", ManifestFile{Language: "typescript"})
	s := string(out)
	if containsAny(s, "// header", "/* block */", "// tail") {
		t.Fatalf("minimal should strip comments; got %q", s)
	}
	if lvl != "minimal" {
		t.Fatalf("level = %q", lvl)
	}
}

func TestApplyFilter_Aggressive_KeepsSignaturesDropsBodies(t *testing.T) {
	in := `export function foo() {
  return 42;
}
export class Bar {
  baz() {
    return "bz";
  }
}
`
	mf := ManifestFile{
		Language:  "typescript",
		LineCount: 8,
		Symbols: []ManifestSymbol{
			{Name: "foo", Kind: "function", StartLine: 1, EndLine: 3, Signature: "export function foo()"},
			{Name: "Bar", Kind: "class", StartLine: 4, EndLine: 8, Signature: "export class Bar"},
		},
		Exports: []string{"foo", "Bar"},
	}
	out, lvl := ApplyFilter([]byte(in), "aggressive", "foo.ts", mf)
	s := string(out)
	if !containsAll(s, "export function foo()", "export class Bar") {
		t.Fatalf("aggressive should keep signatures; got %q", s)
	}
	if containsAny(s, "return 42;", "return \"bz\";") {
		t.Fatalf("aggressive should drop bodies; got %q", s)
	}
	if lvl != "aggressive" {
		t.Fatalf("level = %q", lvl)
	}
}

func TestApplyFilter_AutoDetectsMarkdownAsNone(t *testing.T) {
	// When level=="auto" and the path has a .md extension, ApplyFilter must
	// resolve to "none" and return the content unchanged regardless of the
	// ManifestFile language / LineCount fields.
	in := "# Hello\n\nThis is a markdown document.\n"
	out, lvl := ApplyFilter([]byte(in), "auto", "README.md", ManifestFile{Language: "markdown", LineCount: 9999})
	if string(out) != in {
		t.Fatalf("auto+.md should passthrough; got %q", string(out))
	}
	if lvl != "none" {
		t.Fatalf("auto+.md level = %q, want none", lvl)
	}
	// Also verify with an empty path (no extension) that it falls through to
	// normal resolution (non-empty path behaviour is tested by TestResolveAuto).
	out2, lvl2 := ApplyFilter([]byte(in), "auto", "", ManifestFile{Language: "typescript", LineCount: 50})
	if lvl2 != "minimal" {
		t.Fatalf("auto+empty path, short ts should be minimal; got %q (out=%q)", lvl2, string(out2))
	}
}

func TestResolveAuto(t *testing.T) {
	// non-code → none
	if got := ResolveAuto("README.md", ManifestFile{Language: "markdown", LineCount: 9999}); got != "none" {
		t.Fatalf("README.md auto = %q, want none", got)
	}
	// short code file → minimal
	if got := ResolveAuto("foo.ts", ManifestFile{Language: "typescript", LineCount: 50}); got != "minimal" {
		t.Fatalf("short ts auto = %q, want minimal", got)
	}
	// long code file in supported language → aggressive
	if got := ResolveAuto("foo.ts", ManifestFile{Language: "typescript", LineCount: 500}); got != "aggressive" {
		t.Fatalf("long ts auto = %q, want aggressive", got)
	}
	// unsupported language → minimal even when long
	if got := ResolveAuto("foo.rs", ManifestFile{Language: "rust", LineCount: 500}); got != "minimal" {
		t.Fatalf("rust auto = %q, want minimal (rust unsupported v1)", got)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) < 0 {
			return false
		}
	}
	return true
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
