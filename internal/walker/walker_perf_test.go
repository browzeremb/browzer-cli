package walker

import (
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// TestIgnoreMatcher_CompiledLenSkipsRedundantRecompile pins FR-05 / AC-05:
// repeated matches() calls without an intervening add() must not trigger
// a re-build of the underlying gitignore.GitIgnore. The previous
// dirty-flag implementation already had this property; the compiledLen
// representation makes the invariant explicit and survives a future
// refactor that might add a "force" recompile path.
func TestIgnoreMatcher_CompiledLenSkipsRedundantRecompile(t *testing.T) {
	m := newIgnoreMatcher()
	m.add("*.log\nbuild/\n")

	// First match warms compiledLen to len(lines).
	_ = m.matches("foo.log")
	warmCompiled := m.compiled
	warmLen := m.compiledLen
	if warmLen != len(m.lines) {
		t.Fatalf("after first matches: compiledLen=%d, len(lines)=%d", warmLen, len(m.lines))
	}

	// Subsequent matches must reuse the same compiled instance — no
	// allocation, no recompile.
	for i := 0; i < 100; i++ {
		_ = m.matches("anything.go")
	}
	if m.compiledLen != warmLen {
		t.Fatalf("compiledLen drifted: warm=%d, now=%d", warmLen, m.compiledLen)
	}
	if m.compiled != warmCompiled {
		t.Fatalf("compiled instance was rebuilt without add() — pointer changed")
	}

	// add() growing the slice MUST trigger a recompile on the next
	// matches() call (correctness invariant — nested .gitignore loads
	// rely on this).
	m.add("extra/\n")
	if m.compiledLen == len(m.lines) {
		t.Fatalf("compiledLen still equals len(lines) after add() — recompile would never fire")
	}
	_ = m.matches("extra/foo")
	if m.compiledLen != len(m.lines) {
		t.Fatalf("after second matches: compiledLen=%d, len(lines)=%d (should be in sync)",
			m.compiledLen, len(m.lines))
	}
	if m.compiled == warmCompiled {
		t.Fatalf("compiled instance was NOT rebuilt after add() — correctness violation")
	}
}

// TestIgnoreMatcher_CloneIsIndependent pins the safety guarantee
// WalkRepo's parallel fan-out relies on: cloning a matcher then loading
// additional patterns into the clone must NOT affect the source.
func TestIgnoreMatcher_CloneIsIndependent(t *testing.T) {
	src := newIgnoreMatcher()
	src.add("*.log\n")

	cp := src.clone()
	cp.add("only-in-clone/\n")

	if src.matches("only-in-clone/foo") {
		t.Fatalf("source matcher saw a pattern added only to the clone")
	}
	if !cp.matches("only-in-clone/foo") {
		t.Fatalf("clone failed to honour its own added pattern")
	}
	// Both matchers still agree on the shared root pattern.
	if !src.matches("foo.log") || !cp.matches("foo.log") {
		t.Fatalf("shared root pattern lost during clone")
	}
}

// TestWalkRepo_ParallelDeterminism pins FR-06 / AC-06: WalkRepo's
// top-level fan-out must produce byte-identical output (after the
// internal sort) on every run, even though the per-subtree work order
// is non-deterministic.
func TestWalkRepo_ParallelDeterminism(t *testing.T) {
	root := t.TempDir()

	// Three top-level subdirs the fan-out can race on. Each carries
	// several files so the per-subtree work is non-trivial.
	for _, dir := range []string{"alpha", "bravo", "charlie"} {
		for _, f := range []string{"a.go", "b.go", "c.go"} {
			mustWrite(t, filepath.Join(root, dir, f), "package "+dir+"\n")
		}
	}
	mustWrite(t, filepath.Join(root, "top.go"), "package main\n")

	first, err := WalkRepo(root)
	if err != nil {
		t.Fatalf("first WalkRepo: %v", err)
	}
	for i := 0; i < 5; i++ {
		next, err := WalkRepo(root)
		if err != nil {
			t.Fatalf("WalkRepo run %d: %v", i, err)
		}
		if len(first.Files) != len(next.Files) {
			t.Fatalf("Files count drift: first=%d, run %d=%d", len(first.Files), i, len(next.Files))
		}
		for j := range first.Files {
			if first.Files[j].Path != next.Files[j].Path {
				t.Fatalf("Files[%d].Path drift across runs: first=%q, run %d=%q",
					j, first.Files[j].Path, i, next.Files[j].Path)
			}
		}
		if len(first.Folders) != len(next.Folders) {
			t.Fatalf("Folders count drift: first=%d, run %d=%d", len(first.Folders), i, len(next.Folders))
		}
		for j := range first.Folders {
			if first.Folders[j].Path != next.Folders[j].Path {
				t.Fatalf("Folders[%d].Path drift across runs: first=%q, run %d=%q",
					j, first.Folders[j].Path, i, next.Folders[j].Path)
			}
		}
	}

	// Sanity check the sort itself: Files and Folders must be
	// lexicographically ordered after WalkRepo returns.
	files := make([]string, len(first.Files))
	for i, f := range first.Files {
		files[i] = f.Path
	}
	if !sort.StringsAreSorted(files) {
		t.Fatalf("Files not sorted: %v", files)
	}
	folders := make([]string, len(first.Folders))
	for i, f := range first.Folders {
		folders[i] = f.Path
	}
	if !sort.StringsAreSorted(folders) {
		t.Fatalf("Folders not sorted: %v", folders)
	}
}

// TestIsBinaryFile_ConcurrentReuse pins FR-07 / AC-07: the sync.Pool
// backing the 512-byte probe buffer must be safe for concurrent use.
// 100 goroutines call IsBinaryFile on the same text file; all must
// return false and no race must be detected.
func TestIsBinaryFile_ConcurrentReuse(t *testing.T) {
	textPath := filepath.Join(t.TempDir(), "text.go")
	mustWrite(t, textPath, "package main\n\nfunc main() {}\n")

	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			defer wg.Done()
			if IsBinaryFile(textPath) {
				t.Errorf("text file misclassified as binary under concurrent calls")
			}
		}()
	}
	wg.Wait()
}
