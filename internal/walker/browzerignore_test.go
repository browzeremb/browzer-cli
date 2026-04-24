package walker

import (
	"path/filepath"
	"testing"
)

// TestLoadBrowzerIgnore_MissingFile verifies that a missing .browzerignore
// produces a non-nil, always-false matcher (case 1).
func TestLoadBrowzerIgnore_MissingFile(t *testing.T) {
	root := t.TempDir()
	m := LoadBrowzerIgnore(root)
	if m == nil {
		t.Fatal("expected non-nil matcher for missing file")
	}
	if m.IsIgnored("anything.md") {
		t.Error("expected IsIgnored=false when .browzerignore is absent")
	}
}

// TestLoadBrowzerIgnore_EmptyFile verifies that an empty .browzerignore
// produces an always-false matcher (case 2).
func TestLoadBrowzerIgnore_EmptyFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "")
	m := LoadBrowzerIgnore(root)
	if m.IsIgnored("foo.md") {
		t.Error("expected IsIgnored=false for empty .browzerignore")
	}
}

// TestLoadBrowzerIgnore_SinglePattern verifies that a simple pattern matches
// the intended path but not an unrelated one (case 3).
func TestLoadBrowzerIgnore_SinglePattern(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "foo.md\n")
	m := LoadBrowzerIgnore(root)
	if !m.IsIgnored("foo.md") {
		t.Error("expected IsIgnored=true for foo.md")
	}
	if m.IsIgnored("bar.md") {
		t.Error("expected IsIgnored=false for bar.md")
	}
}

// TestLoadBrowzerIgnore_DirectoryPattern verifies that a directory pattern
// (trailing slash) correctly excludes files nested inside it (case 4).
func TestLoadBrowzerIgnore_DirectoryPattern(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "docs/retrospectives/\n")
	m := LoadBrowzerIgnore(root)
	// Walker passes dir check with trailing slash, file check without.
	if !m.IsIgnored("docs/retrospectives/") {
		t.Error("expected IsIgnored=true for docs/retrospectives/ (dir check)")
	}
	if !m.IsIgnored("docs/retrospectives/x.md") {
		t.Error("expected IsIgnored=true for nested file inside ignored dir")
	}
	if m.IsIgnored("docs/architecture.md") {
		t.Error("expected IsIgnored=false for sibling outside ignored dir")
	}
}

// TestLoadBrowzerIgnore_Negation verifies last-match-wins negation:
// *.md then !readme.md means readme.md is NOT ignored (case 5).
func TestLoadBrowzerIgnore_Negation(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "*.md\n!readme.md\n")
	m := LoadBrowzerIgnore(root)
	if m.IsIgnored("readme.md") {
		t.Error("expected IsIgnored=false for readme.md after negation")
	}
	if !m.IsIgnored("foo.md") {
		t.Error("expected IsIgnored=true for foo.md (matched by *.md, not negated)")
	}
}

// TestLoadBrowzerIgnore_DoubleStarGlob verifies that docs/**/*.md matches
// nested paths under docs/ (case 6).
func TestLoadBrowzerIgnore_DoubleStarGlob(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "docs/**/*.md\n")
	m := LoadBrowzerIgnore(root)
	if !m.IsIgnored("docs/guides/intro.md") {
		t.Error("expected IsIgnored=true for docs/guides/intro.md")
	}
	if !m.IsIgnored("docs/api/reference.md") {
		t.Error("expected IsIgnored=true for docs/api/reference.md")
	}
	if m.IsIgnored("readme.md") {
		t.Error("expected IsIgnored=false for top-level readme.md")
	}
}

// TestLoadBrowzerIgnore_CaseSensitive verifies that pattern matching is
// case-sensitive (mirrors go-gitignore default behavior) (case 7).
func TestLoadBrowzerIgnore_CaseSensitive(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "Foo.md\n")
	m := LoadBrowzerIgnore(root)
	if !m.IsIgnored("Foo.md") {
		t.Error("expected IsIgnored=true for exact-case Foo.md")
	}
	if m.IsIgnored("foo.md") {
		t.Error("expected IsIgnored=false for lowercase foo.md (case-sensitive)")
	}
}

// TestLoadBrowzerIgnore_CommentsAndBlankLines verifies that comments (#) and
// blank lines are treated as no-ops and do not trigger matches (case 8).
func TestLoadBrowzerIgnore_CommentsAndBlankLines(t *testing.T) {
	root := t.TempDir()
	content := "# this is a comment\n\n# another comment\n"
	mustWrite(t, filepath.Join(root, ".browzerignore"), content)
	m := LoadBrowzerIgnore(root)
	if m.IsIgnored("foo.md") {
		t.Error("expected IsIgnored=false when file only has comments and blanks")
	}
	if m.IsIgnored("# this is a comment") {
		t.Error("expected IsIgnored=false: comment lines must not match as patterns")
	}
}

// TestLoadBrowzerIgnore_WildcardExtension verifies that a wildcard extension
// pattern correctly matches all files with that extension (bonus case 9).
func TestLoadBrowzerIgnore_WildcardExtension(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "*.pdf\n")
	m := LoadBrowzerIgnore(root)
	if !m.IsIgnored("report.pdf") {
		t.Error("expected IsIgnored=true for report.pdf")
	}
	if m.IsIgnored("report.md") {
		t.Error("expected IsIgnored=false for report.md")
	}
}

// TestLoadBrowzerIgnore_PresentFlag verifies that the present flag is set
// correctly based on whether .browzerignore exists.
func TestLoadBrowzerIgnore_PresentFlag(t *testing.T) {
	root := t.TempDir()

	// No file: present should be false.
	m := LoadBrowzerIgnore(root)
	if m.present {
		t.Error("expected present=false when file is missing")
	}

	// Create file: present should be true.
	mustWrite(t, filepath.Join(root, ".browzerignore"), "*.log\n")
	m2 := LoadBrowzerIgnore(root)
	if !m2.present {
		t.Error("expected present=true when file exists")
	}
}

// TestWalkDocs_BrowzerIgnoreFilters verifies the end-to-end integration:
// WalkDocs must exclude files matched by .browzerignore.
func TestWalkDocs_BrowzerIgnoreFilters(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".browzerignore"), "docs/private/\n")
	mustWrite(t, filepath.Join(root, "docs", "public.md"), "# public\n")
	mustWrite(t, filepath.Join(root, "docs", "private", "secret.md"), "# secret\n")

	docs, err := WalkDocs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.RelativePath == "docs/private/secret.md" {
			t.Errorf("WalkDocs should not return .browzerignore-excluded file %q", d.RelativePath)
		}
	}
	found := false
	for _, d := range docs {
		if d.RelativePath == "docs/public.md" {
			found = true
		}
	}
	if !found {
		t.Error("WalkDocs should return non-ignored docs/public.md")
	}
}
