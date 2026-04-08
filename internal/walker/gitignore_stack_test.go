package walker

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestWalkRepo_NestedGitignoreStack covers the new layered ignoreMatcher:
// the previous flat-list rebuild was correct but O(N²); the stack
// approach must keep the same observable semantics — root patterns
// apply globally, nested patterns only under their subtree, and a
// nested negation re-includes a parent-ignored file inside its scope.
func TestWalkRepo_NestedGitignoreStack(t *testing.T) {
	root := t.TempDir()

	// Layout:
	//   .gitignore           : ignore *.log + secrets/
	//   keep.go              : kept
	//   debug.log            : ignored by root *.log
	//   secrets/x            : ignored by root secrets/
	//   apps/web/.gitignore  : ignore tmp/ ; un-ignore important.log
	//   apps/web/keep.go     : kept
	//   apps/web/important.log: re-included via negation
	//   apps/web/tmp/junk    : ignored by nested tmp/
	mustWrite(t, filepath.Join(root, ".gitignore"), "*.log\nsecrets/\n")
	mustWrite(t, filepath.Join(root, "keep.go"), "package x\n")
	mustWrite(t, filepath.Join(root, "debug.log"), "noise\n")
	mustWrite(t, filepath.Join(root, "secrets", "x"), "shh\n")
	mustWrite(t, filepath.Join(root, "apps", "web", ".gitignore"), "tmp/\n!important.log\n")
	mustWrite(t, filepath.Join(root, "apps", "web", "keep.go"), "package x\n")
	mustWrite(t, filepath.Join(root, "apps", "web", "important.log"), "ok\n")
	mustWrite(t, filepath.Join(root, "apps", "web", "tmp", "junk"), "trash\n")

	tree, err := WalkRepo(root)
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(tree.Files))
	for _, f := range tree.Files {
		got = append(got, f.Path)
	}
	sort.Strings(got)

	want := []string{
		"apps/web/important.log",
		"apps/web/keep.go",
		"keep.go",
	}
	if !equalSlices(got, want) {
		t.Fatalf("walked files mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
