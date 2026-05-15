package hooks

import (
	"os"
	"path/filepath"
	"sync"
)

// workspaceMaxWalkLevels caps the upward walk performed by
// IsInBrowzerWorkspace so a runaway loop in pathological filesystems
// (e.g. infinite symlink chains) cannot hang the hook.
const workspaceMaxWalkLevels = 20

// workspaceCache caches IsInBrowzerWorkspace results keyed by
// "<homeDir>\x00<cwd>" to eliminate repeated syscalls within the same
// short-lived hook process. A hook binary is invoked once per event and
// exits immediately after — the cache is per-process and never stale.
var workspaceCache sync.Map // key: string → value: bool

// workspaceRootCache caches WorkspaceRootFor results keyed by cwd.
// Same lifetime guarantee as workspaceCache.
var workspaceRootCache sync.Map // key: string → value: string

// IsInBrowzerWorkspace reports whether the supplied cwd is inside a
// Browzer-initialised workspace. The contract is two-fold:
//
//  1. $HOME/.browzer/credentials exists — proves the user is logged in.
//  2. .browzer/config.json is found by walking up from cwd (cap at 20
//     directory levels) — proves we're inside a `browzer init`-ed tree.
//
// Either condition missing → false. The hook surface uses this to skip
// rewrites in unrelated repos so cohabiting Claude Code sessions don't
// trigger spurious browzer-explore rewrites in plain Git checkouts.
//
// Results are cached per-process by (homeDir, cwd) pair — subsequent
// calls with the same arguments perform zero syscalls.
func IsInBrowzerWorkspace(cwd string) bool {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return false
		}
	}

	// Fast-path: $CLAUDE_PROJECT_DIR is set by Claude Code to the project
	// root. When the caller's cwd is at or below that root, we can check
	// it directly rather than walking up from cwd, cutting the walk to a
	// single Stat — but we still verify credentials to honour the login
	// contract. The fast-path does NOT skip the cache key, so subsequent
	// calls are still O(1).
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}

	cacheKey := home + "\x00" + cwd
	if v, ok := workspaceCache.Load(cacheKey); ok {
		return v.(bool)
	}

	result := isInBrowzerWorkspaceSlow(home, cwd)
	workspaceCache.Store(cacheKey, result)
	return result
}

// isInBrowzerWorkspaceSlow is the uncached implementation called exactly
// once per (homeDir, cwd) pair per process lifetime.
func isInBrowzerWorkspaceSlow(home, cwd string) bool {
	if _, err := os.Stat(filepath.Join(home, ".browzer", "credentials")); err != nil {
		return false
	}

	// Fast-path: honour $CLAUDE_PROJECT_DIR as a trusted workspace
	// declaration from Claude Code. If it contains a .browzer/config.json
	// we can skip the upward walk entirely.
	if projDir := os.Getenv("CLAUDE_PROJECT_DIR"); projDir != "" {
		if _, err := os.Stat(filepath.Join(projDir, ".browzer", "config.json")); err == nil {
			return true
		}
	}

	dir := cwd
	for range workspaceMaxWalkLevels {
		if _, err := os.Stat(filepath.Join(dir, ".browzer", "config.json")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	return false
}

// WorkspaceRootFor returns the absolute path of the workspace root
// (the directory containing .browzer/config.json) found by walking up
// from cwd, or "" if no such root is reachable within
// workspaceMaxWalkLevels.
//
// Unlike IsInBrowzerWorkspace this DOES NOT consult $HOME/.browzer/
// credentials — it answers "where does this workspace start?" rather
// than "is the operator logged in?".
//
// Results are cached per-process by cwd — subsequent calls with the
// same argument perform zero syscalls.
func WorkspaceRootFor(cwd string) string {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return ""
		}
	}

	if v, ok := workspaceRootCache.Load(cwd); ok {
		return v.(string)
	}

	result := workspaceRootForSlow(cwd)
	workspaceRootCache.Store(cwd, result)
	return result
}

// workspaceRootForSlow is the uncached implementation called exactly
// once per cwd per process lifetime.
func workspaceRootForSlow(cwd string) string {
	// Fast-path: honour $CLAUDE_PROJECT_DIR as a trusted workspace
	// declaration from Claude Code when its config marker is present.
	if projDir := os.Getenv("CLAUDE_PROJECT_DIR"); projDir != "" {
		if _, err := os.Stat(filepath.Join(projDir, ".browzer", "config.json")); err == nil {
			return projDir
		}
	}

	dir := cwd
	for range workspaceMaxWalkLevels {
		if _, err := os.Stat(filepath.Join(dir, ".browzer", "config.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}
