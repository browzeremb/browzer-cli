package runfilters

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

// Package-level compiled regexes — compiled once at init, not on every call.
var (
	gitCommitRe    = regexp.MustCompile(`\[[\w/.-]+ ([a-f0-9]{7,})\]`)
	gitBinaryRe    = regexp.MustCompile(`^Binary files (.+) and .+ differ`)
	gitDiffHeaderRe = regexp.MustCompile(`^diff --git a/(.+) b/`)
	gitStatusBranchRe = regexp.MustCompile(`(?m)^(?:On branch|HEAD detached at) (\S+)`)
	gitStatusAheadRe  = regexp.MustCompile(`ahead of .+ by (\d+)`)
	gitStatusBehindRe = regexp.MustCompile(`behind .+ by (\d+)`)
	gitLogOnelineRe   = regexp.MustCompile(`^[a-f0-9]{7,40} `)
	gitLogFullRe      = regexp.MustCompile(`^commit [a-f0-9]{40}`)
	gitBranchRefRe    = regexp.MustCompile(`(?m)[^\s]+\s+->\s+([^\s]+)`)
	gitBranchTagRe    = regexp.MustCompile(`\[(?:new branch\s+)?(?:new tag\s+)?([^\s\]]+)\s`)
	gitPullFilesRe    = regexp.MustCompile(`(\d+) files? changed`)
	gitPullInsRe      = regexp.MustCompile(`(\d+) insertions?`)
	gitPullDelRe      = regexp.MustCompile(`(\d+) deletions?`)
)

func init() {
	DefaultRegistry.Register("git", filterGit)
}

// filterGit detects the git subcommand from stdout shape and applies
// token-optimised compression.
func filterGit(stdout []byte) []byte {
	// git diff: starts with "diff --git". Check FIRST so that diffs containing
	// substrings like "Already up to date" or "Everything up-to-date" in their
	// hunks (e.g. a diff of this filter's own source) do not collide with the
	// git-pull / git-push heuristics below.
	if bytes.HasPrefix(bytes.TrimSpace(stdout), []byte("diff --git")) {
		return compressGitDiff(stdout)
	}

	// git push: output contains "remote:" or "Everything up-to-date"
	if bytes.Contains(stdout, []byte("remote:")) || bytes.Contains(stdout, []byte("Everything up-to-date")) {
		branch := extractGitBranch(stdout)
		if branch != "" {
			return []byte("ok " + branch + "\n")
		}
		return []byte("ok\n")
	}

	// git pull: output contains "Updating " or "Already up to date"
	if bytes.Contains(stdout, []byte("Updating ")) || bytes.Contains(stdout, []byte("Already up to date")) {
		return compressGitPull(stdout)
	}

	// git commit: output contains "[branch sha]" pattern
	if m := gitCommitRe.FindSubmatch(stdout); m != nil {
		return []byte("ok " + string(m[1]) + "\n")
	}

	// git add: typically empty output
	if len(bytes.TrimSpace(stdout)) == 0 {
		return []byte("ok\n")
	}

	// git status (long format): contains "On branch"
	if bytes.Contains(stdout, []byte("On branch")) || bytes.Contains(stdout, []byte("HEAD detached")) {
		return compressGitStatus(stdout)
	}

	// git log: heuristic — lines that look like "<sha7+> <subject>" (oneline format)
	// or multi-line log with "commit <sha40>". Compress when >10 lines.
	lines := bytes.Split(bytes.TrimRight(stdout, "\n"), []byte("\n"))
	if isGitLog(lines) && len(lines) > 10 {
		return compressGitLog(lines)
	}

	// Fallback: truncate at 100 lines.
	return truncateLines(lines, 100)
}

// compressGitDiff applies hunk-aware semantic compression to git diff output.
//
// Diffs of ≤30 lines are returned verbatim — they are small enough that a
// reviewer needs the full context and compression would remove more signal than
// it saves tokens. Larger diffs are compacted:
//   - "diff --git" file headers are kept verbatim.
//   - "@@ ..." hunk headers are kept verbatim.
//   - At most 3 leading context lines (space-prefixed) per hunk are kept.
//   - "Binary files ... differ" is replaced with "Bin: <file>".
//   - New-file hunks (all additions, no deletions) collapse to "+ <path> (N lines)".
//   - Deleted-file hunks (all deletions, no additions) collapse to "- <path> (N lines)".
func compressGitDiff(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))
	// Remove trailing empty element from the split if present.
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	// ≤30 lines: return verbatim so reviewers get the full context for tiny diffs.
	if len(lines) <= 30 {
		return stdout
	}

	var out []byte
	appendLine := func(l []byte) {
		out = append(out, l...)
		out = append(out, '\n')
	}

	// Per-hunk caps. Hunks with more than maxModificationLines real changes
	// emit only the first maxModificationLines plus a truncation summary —
	// this is what turns 60-80% ratios into ≤40% on typical PR-sized diffs
	// (~500–1000 changed lines across multiple files).
	const maxModificationLines = 20

	var (
		currentFile      string
		inNewFile        bool // current file-section is new-file (--- /dev/null)
		inDeletedFile    bool // current file-section is deleted-file (+++ /dev/null)
		inHunk           bool
		hunkAddCount     int
		hunkDelCount     int
		contextInHunk    int
		emittedModLines  int      // count of + / - lines already emitted in this hunk
		truncatedSummary bool     // a truncation notice has been emitted for this hunk
		hunkLines        [][]byte // buffered hunk lines for deferred new/deleted collapse
		pendingCollapse  bool     // a potential collapse is in progress
	)

	flushHunk := func() {
		if !pendingCollapse {
			return
		}
		// Decide whether to collapse.
		if inNewFile && hunkDelCount == 0 && hunkAddCount > 0 {
			appendLine([]byte(fmt.Sprintf("+ %s (%d lines)", currentFile, hunkAddCount)))
		} else if inDeletedFile && hunkAddCount == 0 && hunkDelCount > 0 {
			appendLine([]byte(fmt.Sprintf("- %s (%d lines)", currentFile, hunkDelCount)))
		} else {
			for _, hl := range hunkLines {
				out = append(out, hl...)
				out = append(out, '\n')
			}
		}
		hunkLines = hunkLines[:0]
		hunkAddCount = 0
		hunkDelCount = 0
		contextInHunk = 0
		inHunk = false
		pendingCollapse = false
	}

	for _, raw := range lines {
		// Binary file line.
		if m := gitBinaryRe.FindSubmatch(raw); m != nil {
			flushHunk()
			// Extract just the short filename after "a/".
			name := string(m[1])
			if len(name) > 2 && name[:2] == "a/" {
				name = name[2:]
			}
			appendLine([]byte("Bin: " + name))
			continue
		}

		// diff --git header: start of a new file section.
		if m := gitDiffHeaderRe.FindSubmatch(raw); m != nil {
			flushHunk()
			currentFile = string(m[1])
			inNewFile = false
			inDeletedFile = false
			inHunk = false
			appendLine(raw)
			continue
		}

		// --- /dev/null indicates a new file.
		if bytes.Equal(raw, []byte("--- /dev/null")) {
			inNewFile = true
			inDeletedFile = false
			// Don't emit --- /dev/null; the collapse summary covers it.
			continue
		}
		// +++ /dev/null indicates a deleted file.
		if bytes.Equal(raw, []byte("+++ /dev/null")) {
			inNewFile = false
			inDeletedFile = true
			continue
		}

		// Skip other --- / +++ metadata lines (index, mode).
		if bytes.HasPrefix(raw, []byte("--- ")) || bytes.HasPrefix(raw, []byte("+++ ")) {
			continue
		}
		if bytes.HasPrefix(raw, []byte("index ")) || bytes.HasPrefix(raw, []byte("new file mode")) ||
			bytes.HasPrefix(raw, []byte("deleted file mode")) || bytes.HasPrefix(raw, []byte("old mode")) ||
			bytes.HasPrefix(raw, []byte("new mode")) {
			continue
		}

		// @@ hunk header.
		if bytes.HasPrefix(raw, []byte("@@")) {
			flushHunk()
			inHunk = true
			pendingCollapse = inNewFile || inDeletedFile
			contextInHunk = 0
			emittedModLines = 0
			truncatedSummary = false
			if pendingCollapse {
				hunkLines = append(hunkLines, raw)
			} else {
				appendLine(raw)
			}
			continue
		}

		if !inHunk {
			// Any unrecognised line outside a hunk is kept verbatim.
			appendLine(raw)
			continue
		}

		// Inside a hunk.
		emitMod := func(line []byte) {
			if pendingCollapse {
				hunkLines = append(hunkLines, line)
				return
			}
			if emittedModLines < maxModificationLines {
				appendLine(line)
				emittedModLines++
				return
			}
			if !truncatedSummary {
				appendLine([]byte("... (further changes truncated)"))
				truncatedSummary = true
			}
		}
		switch {
		case len(raw) > 0 && raw[0] == '+':
			hunkAddCount++
			emitMod(raw)
		case len(raw) > 0 && raw[0] == '-':
			hunkDelCount++
			emitMod(raw)
		case len(raw) == 0 || raw[0] == ' ':
			// Context line.
			if pendingCollapse {
				hunkLines = append(hunkLines, raw)
			} else if contextInHunk < 3 {
				contextInHunk++
				appendLine(raw)
			}
			// Context lines beyond 3 are dropped (saves tokens on large diffs).
		default:
			if pendingCollapse {
				hunkLines = append(hunkLines, raw)
			} else {
				appendLine(raw)
			}
		}
	}

	flushHunk()
	return out
}

// compressGitStatus collapses `git status` long-form output to a single line.
// Examples:
//
//	"ok branch=main ahead=5 clean"
//	"branch=main ahead=2 behind=1 staged:1 M:3 ??:2"
func compressGitStatus(stdout []byte) []byte {
	branch := "unknown"
	if m := gitStatusBranchRe.FindSubmatch(stdout); m != nil {
		branch = string(m[1])
	}

	var parts []string
	parts = append(parts, "branch="+branch)

	if m := gitStatusAheadRe.FindSubmatch(stdout); m != nil {
		parts = append(parts, "ahead="+string(m[1]))
	}
	if m := gitStatusBehindRe.FindSubmatch(stdout); m != nil {
		parts = append(parts, "behind="+string(m[1]))
	}

	// Count staged, modified, untracked by section headers.
	staged := countStatusSection(stdout, "Changes to be committed")
	modified := countStatusSection(stdout, "Changes not staged for commit")
	untracked := countStatusSection(stdout, "Untracked files")

	if staged > 0 {
		parts = append(parts, fmt.Sprintf("staged:%d", staged))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("M:%d", modified))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("??:%d", untracked))
	}

	clean := bytes.Contains(stdout, []byte("nothing to commit")) ||
		bytes.Contains(stdout, []byte("working tree clean")) ||
		bytes.Contains(stdout, []byte("working directory clean"))
	if clean && staged == 0 && modified == 0 && untracked == 0 {
		return []byte("ok " + joinParts(parts) + " clean\n")
	}
	return []byte(joinParts(parts) + "\n")
}

// countStatusSection counts the number of file-entries under a git status section header.
// Each entry line is indented (starts with a tab or spaces) and follows the header.
func countStatusSection(stdout []byte, header string) int {
	idx := bytes.Index(stdout, []byte(header))
	if idx < 0 {
		return 0
	}
	// Advance past the header line.
	rest := stdout[idx:]
	newline := bytes.IndexByte(rest, '\n')
	if newline < 0 {
		return 0
	}
	rest = rest[newline+1:]

	// Count indented lines until a blank line or unindented line (next section).
	count := 0
	for _, line := range bytes.Split(rest, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if line[0] == '\t' || (len(line) > 1 && line[0] == ' ' && line[1] == ' ') {
			// Skip the "(use ..." hint lines.
			if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("(")) {
				count++
			}
		} else {
			break
		}
	}
	return count
}

func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// isGitLog returns true when lines look like git log output (oneline or full).
func isGitLog(lines [][]byte) bool {
	if len(lines) == 0 {
		return false
	}
	return gitLogOnelineRe.Match(lines[0]) || gitLogFullRe.Match(lines[0])
}

// compressGitLog collapses git log to a summary when output is long.
// Format: "<N> commits; latest: <sha> <subject>"
func compressGitLog(lines [][]byte) []byte {
	n := 0
	for _, l := range lines {
		if len(bytes.TrimSpace(l)) > 0 {
			n++
		}
	}
	latest := string(bytes.TrimSpace(lines[0]))
	// Truncate subject to 72 chars.
	if len(latest) > 72 {
		latest = latest[:72] + "..."
	}
	return []byte(fmt.Sprintf("%d commits; latest: %s\n", n, latest))
}

// extractGitBranch extracts the target branch name from git push output.
func extractGitBranch(stdout []byte) string {
	// "refs/heads/main -> main" or "branch -> remote/branch"
	// Use [^\s]+ to accept legal git refname characters (including + and unicode).
	if m := gitBranchRefRe.FindSubmatch(stdout); m != nil {
		return string(m[1])
	}
	// "[new branch] main -> main" style or "[branch sha]" from commit.
	// Use [^\s\]]+ to accept legal git refname characters (including + and unicode).
	if m := gitBranchTagRe.FindSubmatch(stdout); m != nil {
		return string(m[1])
	}
	return ""
}

// compressGitPull collapses git pull output to a single summary line.
func compressGitPull(stdout []byte) []byte {
	if bytes.Contains(stdout, []byte("Already up to date")) {
		return []byte("ok (already up to date)\n")
	}
	if m := gitPullFilesRe.FindSubmatch(stdout); m != nil {
		ins := gitPullInsRe.FindSubmatch(stdout)
		del := gitPullDelRe.FindSubmatch(stdout)
		msg := "ok " + string(m[1]) + " files"
		if ins != nil {
			msg += " +" + string(ins[1])
		}
		if del != nil {
			msg += " -" + string(del[1])
		}
		return []byte(msg + "\n")
	}
	return []byte("ok\n")
}

// truncateLines joins lines up to max; appends a truncation notice when over.
func truncateLines(lines [][]byte, max int) []byte {
	if len(lines) <= max {
		return bytes.Join(lines, []byte("\n"))
	}
	notice := []byte(fmt.Sprintf("... (%s lines truncated)", strconv.Itoa(len(lines)-max)))
	truncated := append(lines[:max:max], notice)
	return bytes.Join(truncated, []byte("\n"))
}
