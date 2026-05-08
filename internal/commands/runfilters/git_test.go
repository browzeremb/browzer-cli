package runfilters

import (
	"bytes"
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

func TestFilterGit_Diff_TruncatedAt150Lines(t *testing.T) {
	// Build a diff with 200 lines.
	var sb strings.Builder
	sb.WriteString("diff --git a/foo.go b/foo.go\n")
	sb.WriteString("index 000..111 100644\n")
	for i := 0; i < 198; i++ {
		sb.WriteString("+line content here\n")
	}
	stdout := []byte(sb.String())

	got := filterGit(stdout)
	lines := bytes.Split(got, []byte("\n"))

	// Last non-empty line should be the truncation notice.
	var lastNonEmpty string
	for i := len(lines) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(lines[i])) > 0 {
			lastNonEmpty = string(lines[i])
			break
		}
	}
	if !strings.Contains(lastNonEmpty, "truncated") {
		t.Errorf("git diff: expected truncation notice, got last line %q", lastNonEmpty)
	}

	// Should not exceed 150 content lines + 1 notice line.
	if len(lines) > 152 {
		t.Errorf("git diff: got %d lines, want <=152", len(lines))
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
