// Package git wraps the few git plumbing commands the CLI needs:
// findGitRoot and checkStaleness. We shell out instead of vendoring
// go-git because the surface is tiny and we want exact byte-compatible
// behavior with what `git` reports.
package git

import (
	"bytes"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// FindGitRoot returns the absolute path to the git repo containing
// cwd, or empty string if not in a repo. Mirrors `git rev-parse
// --show-toplevel`.
func FindGitRoot(cwd string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// HEAD returns the current HEAD commit hash, or empty string on error.
func HEAD(repoRoot string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Staleness reports how many commits HEAD is ahead of lastSyncCommit.
type Staleness struct {
	Stale         bool
	CommitsBehind int
	CurrentHead   string
}

// CheckStaleness returns the number of commits between lastSyncCommit
// and HEAD via `git rev-list --count <lastSync>..HEAD`. Fails open
// (returns Stale=false) when git is unavailable or lastSyncCommit is
// empty.
func CheckStaleness(repoRoot, lastSyncCommit string) Staleness {
	head := HEAD(repoRoot)
	if head == "" || lastSyncCommit == "" {
		return Staleness{CurrentHead: head}
	}
	cmd := exec.Command("git", "rev-list", "--count", lastSyncCommit+"..HEAD")
	cmd.Dir = repoRoot
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return Staleness{CurrentHead: head}
	}
	n, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		return Staleness{CurrentHead: head}
	}
	return Staleness{
		Stale:         n > 0,
		CommitsBehind: n,
		CurrentHead:   head,
	}
}

// ErrNotARepo is returned by helpers that strictly require a git repo.
var ErrNotARepo = errors.New("not inside a git repository")
