package runfilters

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestFilterGit_Push_RemoteWithBranch(t *testing.T) {
	// Typical `git push` output containing "remote:" and a branch ref line.
	stdout := []byte(`Enumerating objects: 5, done.
Counting objects: 100% (5/5), done.
Writing objects: 100% (3/3), 300 bytes | 300.00 KiB/s, done.
remote: Resolving deltas: 100% (1/1), done.
To github.com:org/repo.git
   refs/heads/main -> main
`)
	got := filterGit(stdout)
	want := "ok main\n"
	if string(got) != want {
		t.Errorf("git push: got %q, want %q", got, want)
	}
}

func TestFilterGit_Push_EverythingUpToDate(t *testing.T) {
	stdout := []byte("Everything up-to-date\n")
	got := filterGit(stdout)
	// No branch extractable from this output — falls back to "ok\n"
	want := "ok\n"
	if string(got) != want {
		t.Errorf("git push (up-to-date): got %q, want %q", got, want)
	}
}

func TestFilterGit_Pull_AlreadyUpToDate(t *testing.T) {
	stdout := []byte("Already up to date.\n")
	got := filterGit(stdout)
	want := "ok (already up to date)\n"
	if string(got) != want {
		t.Errorf("git pull (already up to date): got %q, want %q", got, want)
	}
}

func TestFilterGit_Pull_WithStats(t *testing.T) {
	stdout := []byte(`Updating abc1234..def5678
Fast-forward
 foo.go | 10 ++++++++++
 bar.go |  2 --
 3 files changed, 10 insertions(+), 2 deletions(-)
`)
	got := filterGit(stdout)
	want := "ok 3 files +10 -2\n"
	if string(got) != want {
		t.Errorf("git pull (stats): got %q, want %q", got, want)
	}
}

func TestFilterGit_Pull_WithStatsNoInsertions(t *testing.T) {
	stdout := []byte(`Updating abc1234..def5678
Fast-forward
 foo.go | 2 --
 1 file changed, 2 deletions(-)
`)
	got := filterGit(stdout)
	want := "ok 1 files -2\n"
	if string(got) != want {
		t.Errorf("git pull (deletions only): got %q, want %q", got, want)
	}
}

func TestFilterGit_Commit_WithSHA(t *testing.T) {
	stdout := []byte("[main abc1234] feat: add something\n 1 file changed, 5 insertions(+)\n")
	got := filterGit(stdout)
	want := "ok abc1234\n"
	if string(got) != want {
		t.Errorf("git commit: got %q, want %q", got, want)
	}
}

func TestFilterGit_Commit_WithLongSHA(t *testing.T) {
	stdout := []byte("[feature/my-branch abcdef1234567] fix: something\n")
	got := filterGit(stdout)
	want := "ok abcdef1234567\n"
	if string(got) != want {
		t.Errorf("git commit (long sha): got %q, want %q", got, want)
	}
}

func TestFilterGit_Diff_HunkAware_LargeDiff(t *testing.T) {
	// Build a diff >200 lines using new-file additions (--- /dev/null pattern).
	// New-file hunks collapse to a single "+" summary line, giving massive reduction.
	var sb strings.Builder

	// File 1: new file with 120 added lines.
	sb.WriteString("diff --git a/newfile1.go b/newfile1.go\n")
	sb.WriteString("new file mode 100644\n")
	sb.WriteString("index 0000000..abc1234\n")
	sb.WriteString("--- /dev/null\n")
	sb.WriteString("+++ b/newfile1.go\n")
	sb.WriteString("@@ -0,0 +1,120 @@\n")
	for i := 0; i < 120; i++ {
		sb.WriteString("+package main // line " + strings.Repeat("x", 30) + "\n")
	}

	// File 2: new file with 120 added lines.
	sb.WriteString("diff --git a/newfile2.go b/newfile2.go\n")
	sb.WriteString("new file mode 100644\n")
	sb.WriteString("index 0000000..def5678\n")
	sb.WriteString("--- /dev/null\n")
	sb.WriteString("+++ b/newfile2.go\n")
	sb.WriteString("@@ -0,0 +1,120 @@\n")
	for i := 0; i < 120; i++ {
		sb.WriteString("+func Foo() {} // line " + strings.Repeat("y", 30) + "\n")
	}

	// File 3: normal modification hunk (ensures at least one @@ header is preserved).
	sb.WriteString("diff --git a/existing.go b/existing.go\n")
	sb.WriteString("index aaa..bbb 100644\n")
	sb.WriteString("--- a/existing.go\n")
	sb.WriteString("+++ b/existing.go\n")
	sb.WriteString("@@ -5,4 +5,4 @@\n")
	sb.WriteString(" unchanged context\n")
	sb.WriteString("-old line\n")
	sb.WriteString("+new line\n")

	stdout := []byte(sb.String())
	rawLen := len(stdout)

	got := filterGit(stdout)
	filteredLen := len(got)

	// New-file collapses to 2 summary lines (one per file) + 2 diff headers + 2 @@ headers.
	// Should achieve >60 % reduction.
	ratio := float64(filteredLen) / float64(rawLen)
	if ratio > 0.40 {
		t.Errorf("git diff hunk-aware: ratio=%.1f%% (want ≤40%%); raw=%d filtered=%d",
			ratio*100, rawLen, filteredLen)
	}

	// Must preserve at least one @@ header.
	if !bytes.Contains(got, []byte("@@")) {
		t.Error("git diff hunk-aware: expected at least one @@ header preserved")
	}

	// Must preserve diff --git headers.
	if !bytes.Contains(got, []byte("diff --git a/")) {
		t.Error("git diff hunk-aware: expected diff --git header preserved")
	}
}

func TestFilterGit_Diff_ShortOutputPassesThrough(t *testing.T) {
	stdout := []byte("diff --git a/foo.go b/foo.go\nindex 000..111 100644\n+one line\n")
	got := filterGit(stdout)
	if !bytes.Contains(got, []byte("+one line")) {
		t.Errorf("git diff (short): expected content to pass through, got %q", got)
	}
	if bytes.Contains(got, []byte("truncated")) {
		t.Errorf("git diff (short): unexpected truncation notice")
	}
}

// TestFilterGit_Diff_DoesNotCollideWithPullStrings asserts that a diff whose
// content contains literal "Already up to date" or "Updating " substrings
// (e.g. a diff of source code that mentions those strings) is still routed
// to compressGitDiff, not to the git-pull / git-push heuristics.
func TestFilterGit_Diff_DoesNotCollideWithPullStrings(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("diff --git a/filter.go b/filter.go\n")
	sb.WriteString("index 111..222 100644\n")
	sb.WriteString("--- a/filter.go\n")
	sb.WriteString("+++ b/filter.go\n")
	sb.WriteString("@@ -1,40 +1,40 @@\n")
	for i := 0; i < 35; i++ {
		sb.WriteString(" context line\n")
	}
	sb.WriteString("-\t\tif bytes.Contains(stdout, []byte(\"Already up to date\")) {\n")
	sb.WriteString("+\t\tif bytes.Contains(stdout, []byte(\"Updating \")) {\n")
	input := []byte(sb.String())
	got := filterGit(input)
	if !bytes.Contains(got, []byte("diff --git")) {
		t.Errorf("diff with pull-marker content: expected hunk-aware compression, got %q", got)
	}
	if bytes.Equal(got, []byte("ok (already up to date)\n")) {
		t.Errorf("diff with pull-marker content: routed to compressGitPull, expected compressGitDiff")
	}
}

// TestFilterGit_Diff_LeThirtyLines asserts 0 % compression on ≤30 line diffs.
func TestFilterGit_Diff_LeThirtyLines(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("diff --git a/small.go b/small.go\n")
	sb.WriteString("index 111..222 100644\n")
	sb.WriteString("--- a/small.go\n")
	sb.WriteString("+++ b/small.go\n")
	sb.WriteString("@@ -1,3 +1,3 @@\n")
	sb.WriteString(" unchanged\n")
	sb.WriteString("-old line\n")
	sb.WriteString("+new line\n")
	// 8 lines total — well under 30.
	input := []byte(sb.String())
	got := filterGit(input)
	if string(got) != string(input) {
		t.Errorf("git diff ≤30 lines: expected pass-through, input=%q got=%q", input, got)
	}
}

// TestFilterGit_Diff_BinaryFileSummary verifies binary lines are replaced with "Bin: <file>".
func TestFilterGit_Diff_BinaryFileSummary(t *testing.T) {
	// Construct a diff long enough to trigger compression (>30 lines).
	var sb strings.Builder
	sb.WriteString("diff --git a/image.png b/image.png\n")
	sb.WriteString("index abc..def 100644\n")
	sb.WriteString("Binary files a/image.png and b/image.png differ\n")
	// Pad to >30 lines.
	for i := 0; i < 28; i++ {
		sb.WriteString("diff --git a/file" + strconv.Itoa(i) + ".go b/file" + strconv.Itoa(i) + ".go\n")
	}
	input := []byte(sb.String())
	got := filterGit(input)

	if bytes.Contains(got, []byte("Binary files")) {
		t.Error("expected 'Binary files ... differ' line to be replaced")
	}
	if !bytes.Contains(got, []byte("Bin: image.png")) {
		t.Errorf("expected 'Bin: image.png' in output, got: %q", got)
	}
}

// TestFilterGit_Diff_ContextLinesLimited verifies at most 3 context lines kept per hunk.
func TestFilterGit_Diff_ContextLinesLimited(t *testing.T) {
	// Build a diff large enough to trigger compression, with 6 context lines in the hunk.
	var sb strings.Builder
	sb.WriteString("diff --git a/ctx.go b/ctx.go\n")
	sb.WriteString("index aaa..bbb 100644\n")
	sb.WriteString("--- a/ctx.go\n")
	sb.WriteString("+++ b/ctx.go\n")
	sb.WriteString("@@ -1,10 +1,10 @@\n")
	for i := 0; i < 6; i++ {
		sb.WriteString(" context line " + strconv.Itoa(i) + "\n")
	}
	for i := 0; i < 50; i++ {
		sb.WriteString("+added " + strconv.Itoa(i) + "\n")
	}
	// Pad to >30 lines total.
	for sb.Len() < 40*30 {
		sb.WriteString("+pad\n")
	}

	input := []byte(sb.String())
	got := filterGit(input)

	// Count kept context lines (space-prefixed lines after @@).
	ctxCount := 0
	inHunk := false
	for _, line := range bytes.Split(got, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("@@")) {
			inHunk = true
			ctxCount = 0
			continue
		}
		if inHunk && len(line) > 0 && line[0] == ' ' {
			ctxCount++
		}
	}
	if ctxCount > 3 {
		t.Errorf("expected ≤3 context lines per hunk, got %d", ctxCount)
	}
}

func TestFilterGit_Log_Oneline_PassesThrough(t *testing.T) {
	// git log --oneline output: short lines, no special marker.
	stdout := []byte("abc1234 feat: add foo\ndef5678 fix: bar baz\n1234abc chore: cleanup\n")
	got := filterGit(stdout)
	if !bytes.Equal(got, stdout) {
		// May differ by trailing newline handling — just check content is preserved.
		if !bytes.Contains(got, []byte("feat: add foo")) {
			t.Errorf("git log: content not preserved; got %q", got)
		}
	}
}

func TestFilterGit_Add_EmptyOutput(t *testing.T) {
	stdout := []byte("")
	got := filterGit(stdout)
	want := "ok\n"
	if string(got) != want {
		t.Errorf("git add (empty): got %q, want %q", got, want)
	}
}

func TestFilterGit_Add_WhitespaceOnlyOutput(t *testing.T) {
	stdout := []byte("   \n\t\n")
	got := filterGit(stdout)
	want := "ok\n"
	if string(got) != want {
		t.Errorf("git add (whitespace): got %q, want %q", got, want)
	}
}

func TestFilterGit_Status_Short_PassesThrough(t *testing.T) {
	stdout := []byte("M  foo.go\n?? bar.go\n A baz.go\n")
	got := filterGit(stdout)
	if !bytes.Contains(got, []byte("foo.go")) {
		t.Errorf("git status --short: content not preserved; got %q", got)
	}
}

func TestExtractGitBranch_PlusInBranchName(t *testing.T) {
	// FR-14: branches with '+' (valid per git check-ref-format) must match.
	stdout := []byte("[new branch] feat/foo+bar -> origin/feat/foo+bar\n")
	got := extractGitBranch(stdout)
	if got != "origin/feat/foo+bar" && got != "feat/foo+bar" {
		t.Errorf("extractGitBranch: branch with + not extracted, got %q", got)
	}
}

// TestExtractGitBranch_CommitBracketStyle verifies the "[branch sha]" pattern
// (no "->") triggers the gitBranchTagRe path.
func TestExtractGitBranch_CommitBracketStyle(t *testing.T) {
	// git commit output: "[main abc1234] feat: something" — no "->" present,
	// so gitBranchRefRe won't match; gitBranchTagRe captures "main".
	got := extractGitBranch([]byte("[main abc1234] feat: something\n"))
	if got != "main" {
		t.Errorf("extractGitBranch (commit bracket): got %q, want %q", got, "main")
	}
}

// TestExtractGitBranch_NoMatch verifies that unrecognised push output returns "".
func TestExtractGitBranch_NoMatch(t *testing.T) {
	got := extractGitBranch([]byte("some unrecognised output that matches neither pattern"))
	if got != "" {
		t.Errorf("extractGitBranch (no match): got %q, want empty string", got)
	}
}

func TestFilterGit_RegisteredInDefaultRegistry(t *testing.T) {
	fn := DefaultRegistry.Resolve([]string{"git", "status"})
	if fn == nil {
		t.Fatal("expected 'git' filter to be registered in DefaultRegistry via init()")
	}
}

func TestTruncateLines_ExactlyAtMax(t *testing.T) {
	lines := make([][]byte, 10)
	for i := range lines {
		lines[i] = []byte("line")
	}
	got := truncateLines(lines, 10)
	if bytes.Contains(got, []byte("truncated")) {
		t.Error("truncateLines: should not add truncation notice when at max")
	}
}

func TestTruncateLines_OverMax(t *testing.T) {
	lines := make([][]byte, 15)
	for i := range lines {
		lines[i] = []byte("line")
	}
	got := truncateLines(lines, 10)
	if !bytes.Contains(got, []byte("truncated")) {
		t.Error("truncateLines: expected truncation notice when over max")
	}
	if !bytes.Contains(got, []byte("5")) {
		t.Error("truncateLines: expected count of truncated lines in notice")
	}
}

// TestCompressGitStatus_CleanBranch verifies the compact "ok branch=main clean" form.
func TestCompressGitStatus_CleanBranch(t *testing.T) {
	stdout := []byte(`On branch main
Your branch is up to date with 'origin/main'.

nothing to commit, working tree clean
`)
	got := compressGitStatus(stdout)
	want := "ok branch=main clean\n"
	if string(got) != want {
		t.Errorf("compressGitStatus(clean): got %q, want %q", got, want)
	}
}

// TestCompressGitStatus_DirtyWithStagedModifiedUntracked verifies that
// staged, modified, and untracked counts are surfaced when the tree is dirty.
func TestCompressGitStatus_DirtyWithStagedModifiedUntracked(t *testing.T) {
	stdout := []byte(`On branch feature/abc
Your branch is ahead of 'origin/feature/abc' by 2 commits.
  (use "git push" to publish your local commits)

Changes to be committed:
  (use "git restore --staged <file>..." to unstage)
	modified:   foo.go
	new file:   bar.go

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
	modified:   baz.go

Untracked files:
  (use "git add <file>..." to include in what will be committed)
	qux.go
`)
	got := compressGitStatus(stdout)
	s := string(got)
	// Must carry branch, ahead, staged, M, untracked.
	for _, want := range []string{"branch=feature/abc", "ahead=2", "staged:2", "M:1", "??:1"} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("compressGitStatus(dirty): missing %q in %q", want, s)
		}
	}
	// Must NOT be clean.
	if bytes.Contains(got, []byte("clean")) {
		t.Errorf("compressGitStatus(dirty): got 'clean' for dirty tree: %q", s)
	}
}

// TestCountStatusSection_NoHeader returns 0 when header is absent.
func TestCountStatusSection_NoHeader(t *testing.T) {
	n := countStatusSection([]byte("nothing to commit\n"), "Changes to be committed")
	if n != 0 {
		t.Errorf("countStatusSection (absent header): got %d, want 0", n)
	}
}

// TestCountStatusSection_WithEntries counts indented entries after a header.
func TestCountStatusSection_WithEntries(t *testing.T) {
	stdout := []byte(`Changes to be committed:
  (use "git restore --staged...")
	modified:   file1.go
	new file:   file2.go

Changes not staged for commit:
`)
	n := countStatusSection(stdout, "Changes to be committed")
	if n != 2 {
		t.Errorf("countStatusSection: got %d, want 2", n)
	}
}

// TestJoinParts_Empty returns empty string for zero parts.
func TestJoinParts_Empty(t *testing.T) {
	got := joinParts(nil)
	if got != "" {
		t.Errorf("joinParts(nil): got %q, want empty", got)
	}
}

// TestJoinParts_Single returns the part without a leading space.
func TestJoinParts_Single(t *testing.T) {
	got := joinParts([]string{"branch=main"})
	if got != "branch=main" {
		t.Errorf("joinParts(single): got %q, want %q", got, "branch=main")
	}
}

// TestJoinParts_Multiple joins with single spaces.
func TestJoinParts_Multiple(t *testing.T) {
	got := joinParts([]string{"branch=main", "ahead=3", "clean"})
	want := "branch=main ahead=3 clean"
	if got != want {
		t.Errorf("joinParts(multiple): got %q, want %q", got, want)
	}
}

// TestCompressGitLog_BasicOneline verifies the "<N> commits; latest: <sha>" shape.
func TestCompressGitLog_BasicOneline(t *testing.T) {
	lines := [][]byte{
		[]byte("abc1234 feat: add token economy"),
		[]byte("def5678 fix: ratio precision"),
		[]byte("1234abc chore: cleanup"),
	}
	got := compressGitLog(lines)
	s := string(got)
	if !bytes.Contains(got, []byte("3 commits")) {
		t.Errorf("compressGitLog: expected '3 commits', got %q", s)
	}
	if !bytes.Contains(got, []byte("abc1234 feat: add token economy")) {
		t.Errorf("compressGitLog: expected latest sha in output, got %q", s)
	}
}

// TestCompressGitLog_SubjectTruncatedAt72 verifies that subjects longer than
// 72 chars are capped with "...".
func TestCompressGitLog_SubjectTruncatedAt72(t *testing.T) {
	long := "abc1234 " + strings.Repeat("x", 70) // total > 72 after leading chars
	lines := [][]byte{[]byte(long)}
	got := compressGitLog(lines)
	if !bytes.Contains(got, []byte("...")) {
		t.Errorf("compressGitLog (long subject): expected truncation '...', got %q", got)
	}
	// Truncated segment must be ≤ 72 + len("...") = 75 chars.
	s := string(bytes.TrimSpace(got))
	// Strip the "N commits; latest: " prefix.
	const prefix = "1 commits; latest: "
	if !bytes.HasPrefix(got, []byte(prefix)) {
		t.Fatalf("compressGitLog: unexpected prefix: %q", s)
	}
	subject := s[len(prefix):]
	if len(subject) > 75 {
		t.Errorf("compressGitLog: truncated subject length %d > 75, got %q", len(subject), subject)
	}
}

// TestIsGitLog_OnelineMatch verifies that short-SHA lines are recognised.
func TestIsGitLog_OnelineMatch(t *testing.T) {
	if !isGitLog([][]byte{[]byte("abc1234 feat: something")}) {
		t.Error("isGitLog: expected true for oneline SHA line")
	}
}

// TestIsGitLog_FullCommitMatch verifies that "commit <40hex>" is recognised.
func TestIsGitLog_FullCommitMatch(t *testing.T) {
	sha := strings.Repeat("a", 40)
	if !isGitLog([][]byte{[]byte("commit " + sha)}) {
		t.Error("isGitLog: expected true for full-format commit header")
	}
}

// TestIsGitLog_Empty returns false for zero lines.
func TestIsGitLog_Empty(t *testing.T) {
	if isGitLog(nil) {
		t.Error("isGitLog(nil): expected false")
	}
	if isGitLog([][]byte{}) {
		t.Error("isGitLog([]): expected false")
	}
}

// TestIsGitLog_NoMatch returns false for non-log output.
func TestIsGitLog_NoMatch(t *testing.T) {
	if isGitLog([][]byte{[]byte("some random output")}) {
		t.Error("isGitLog: expected false for non-log line")
	}
}

// TestFilterGit_Diff_DeletedFileCollapse verifies that a deleted-file diff
// (+++ /dev/null path) compresses to "- <path> (N lines)" per the contract.
func TestFilterGit_Diff_DeletedFileCollapse(t *testing.T) {
	var sb strings.Builder

	// File 1: deleted file with 50 removed lines — should collapse.
	sb.WriteString("diff --git a/removed.go b/removed.go\n")
	sb.WriteString("deleted file mode 100644\n")
	sb.WriteString("index abc1234..0000000\n")
	sb.WriteString("--- a/removed.go\n")
	sb.WriteString("+++ /dev/null\n")
	sb.WriteString("@@ -1,50 +0,0 @@\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("-deleted line " + strconv.Itoa(i) + "\n")
	}

	// File 2: normal modification so total line count exceeds 30 and triggers compression.
	sb.WriteString("diff --git a/kept.go b/kept.go\n")
	sb.WriteString("index aaa..bbb 100644\n")
	sb.WriteString("--- a/kept.go\n")
	sb.WriteString("+++ b/kept.go\n")
	sb.WriteString("@@ -1,2 +1,2 @@\n")
	sb.WriteString(" ctx\n")
	sb.WriteString("-old\n")
	sb.WriteString("+new\n")

	input := []byte(sb.String())
	got := filterGit(input)

	// The deleted-file section must collapse to a single "- removed.go (N lines)" line.
	want := "- removed.go (50 lines)"
	if !bytes.Contains(got, []byte(want)) {
		t.Errorf("deleted-file collapse: expected %q in output, got:\n%s", want, got)
	}

	// The raw deletion lines must NOT appear in compressed output.
	if bytes.Contains(got, []byte("-deleted line")) {
		t.Error("deleted-file collapse: raw deletion lines should not appear in compressed output")
	}
}
