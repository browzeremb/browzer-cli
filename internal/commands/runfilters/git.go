package runfilters

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

func init() {
	DefaultRegistry.Register("git", filterGit)
}

// filterGit detects the git subcommand from stdout shape and applies
// token-optimised compression.
func filterGit(stdout []byte) []byte {
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
	if m := regexp.MustCompile(`\[[\w/.-]+ ([a-f0-9]{7,})\]`).FindSubmatch(stdout); m != nil {
		return []byte("ok " + string(m[1]) + "\n")
	}

	// git diff: starts with "diff --git"
	if bytes.HasPrefix(bytes.TrimSpace(stdout), []byte("diff --git")) {
		lines := bytes.Split(stdout, []byte("\n"))
		return truncateLines(lines, 150)
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

// compressGitStatus collapses `git status` long-form output to a single line.
// Examples:
//
//	"ok branch=main ahead=5 clean"
//	"branch=main ahead=2 behind=1 staged:1 M:3 ??:2"
func compressGitStatus(stdout []byte) []byte {
	branch := "unknown"
	if m := regexp.MustCompile(`(?m)^(?:On branch|HEAD detached at) (\S+)`).FindSubmatch(stdout); m != nil {
		branch = string(m[1])
	}

	var parts []string
	parts = append(parts, "branch="+branch)

	if m := regexp.MustCompile(`ahead of .+ by (\d+)`).FindSubmatch(stdout); m != nil {
		parts = append(parts, "ahead="+string(m[1]))
	}
	if m := regexp.MustCompile(`behind .+ by (\d+)`).FindSubmatch(stdout); m != nil {
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
	// Oneline format: "<sha7+> <subject>"
	onelineRe := regexp.MustCompile(`^[a-f0-9]{7,40} `)
	// Full format: "commit <sha40>"
	fullRe := regexp.MustCompile(`^commit [a-f0-9]{40}`)
	return onelineRe.Match(lines[0]) || fullRe.Match(lines[0])
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
	re := regexp.MustCompile(`(?m)[^\s]+\s+->\s+([^\s]+)`)
	if m := re.FindSubmatch(stdout); m != nil {
		return string(m[1])
	}
	// "[new branch] main -> main" style or "[branch sha]" from commit.
	// Use [^\s\]]+ to accept legal git refname characters (including + and unicode).
	re2 := regexp.MustCompile(`\[(?:new branch\s+)?(?:new tag\s+)?([^\s\]]+)\s`)
	if m := re2.FindSubmatch(stdout); m != nil {
		return string(m[1])
	}
	return ""
}

// compressGitPull collapses git pull output to a single summary line.
func compressGitPull(stdout []byte) []byte {
	if bytes.Contains(stdout, []byte("Already up to date")) {
		return []byte("ok (already up to date)\n")
	}
	re := regexp.MustCompile(`(\d+) files? changed`)
	if m := re.FindSubmatch(stdout); m != nil {
		ins := regexp.MustCompile(`(\d+) insertions?`).FindSubmatch(stdout)
		del := regexp.MustCompile(`(\d+) deletions?`).FindSubmatch(stdout)
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
