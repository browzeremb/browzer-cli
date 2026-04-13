// Package git wraps the few git plumbing commands the CLI needs:
// findGitRoot and checkStaleness. We shell out instead of vendoring
// go-git because the surface is tiny and we want exact byte-compatible
// behavior with what `git` reports.
package git

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// FindGitRoot returns the absolute path to the git repo containing
// cwd, or empty string if not in a repo. Mirrors `git rev-parse
// --show-toplevel`.
//
// On macOS (case-insensitive HFS+/APFS) git returns the real-cased
// path (e.g. /Users/x/Desktop/repo) while os.Getwd may return a
// differently-cased variant (e.g. /Users/x/desktop/repo). This
// breaks filepath.Rel later. We canonicalize via RealPath so all
// downstream path operations use a consistent prefix.
func FindGitRoot(cwd string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return RealPath(strings.TrimSpace(string(out)))
}

// RealPath resolves a path to its canonical form with correct
// filesystem casing. On macOS (case-insensitive HFS+/APFS) this
// walks each path component via os.ReadDir to find the real name
// the filesystem stored. On other platforms it falls back to
// filepath.EvalSymlinks. Use this before filepath.Rel when both
// operands may come from different sources (git vs os.Getwd).
func RealPath(p string) string {
	if runtime.GOOS != "darwin" {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return real
		}
		return p
	}
	// Walk component by component, reading the real-cased name
	// from the directory listing at each level.
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	parts := strings.Split(filepath.Clean(rest), string(filepath.Separator))
	resolved := vol + string(filepath.Separator)
	for _, part := range parts {
		if part == "" {
			continue
		}
		entries, err := os.ReadDir(resolved)
		if err != nil {
			return p // bail — return original
		}
		found := false
		lower := strings.ToLower(part)
		for _, e := range entries {
			if strings.ToLower(e.Name()) == lower {
				resolved = filepath.Join(resolved, e.Name())
				found = true
				break
			}
		}
		if !found {
			return p // bail — component doesn't exist
		}
	}
	return resolved
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
