package hooks

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// sessionSanitizeRE matches any character outside the sentinel-safe
// alphabet [A-Za-z0-9_-]. Matched runs are replaced with `_` so the
// resolved session key is always a safe filename component.
var sessionSanitizeRE = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// resolveSessionKey derives a stable, filename-safe identifier for the
// current session. Priority order mirrors the JS source:
//
//  1. CLAUDE_SESSION_ID env var (set by Claude Code)
//  2. sha1(CLAUDE_PROJECT_DIR)[:12]
//  3. parent process id
func resolveSessionKey() string {
	if sid := os.Getenv("CLAUDE_SESSION_ID"); sid != "" {
		return sessionSanitizeRE.ReplaceAllString(sid, "_")
	}
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		sum := sha1.Sum([]byte(dir))
		return hex.EncodeToString(sum[:])[:12]
	}
	return strconv.Itoa(os.Getppid())
}

// sentinelPathFor returns the absolute path of the sentinel file used to
// track a once-per-session emission keyed by label. Sentinels live under
// $HOME/.browzer/session/ so they survive within a single Claude Code
// session but are isolated from other tmpfs-cleaning pressure.
//
// Returns an empty string when $HOME cannot be resolved — caller treats
// that as "err toward emission" (banner re-fires).
func sentinelPathFor(label string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	sid := resolveSessionKey()
	safeLabel := sessionSanitizeRE.ReplaceAllString(label, "_")
	return filepath.Join(home, ".browzer", "session", safeLabel+"-"+sid+".flag")
}

// SessionBannerEmittedOnce returns true iff this is the first emission of
// the named banner in the current session. Subsequent calls in the same
// session return false. Designed for the "once-per-session" banner
// dedup path consumed by browzer-rewrite-bash, browzer-suggest-grep,
// browzer-block-glob (soft mode), etc.
//
// Concurrency contract: the create is done with O_CREATE|O_EXCL|O_WRONLY
// so concurrent callers race on rename(2)-style atomicity at the
// filesystem layer. The winner gets `true`; every loser gets `false`
// because EEXIST surfaces.
//
// Failure mode (filesystem unwritable, $HOME missing): err toward
// emission — return true so the banner fires. Better to banner twice
// than never.
func SessionBannerEmittedOnce(label string) bool {
	path := sentinelPathFor(label)
	if path == "" {
		return true // err toward emission
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return true
	}

	// O_CREATE|O_EXCL is the canonical TOCTOU-safe create. If two
	// goroutines / processes race here, exactly one succeeds and the
	// rest see EEXIST. Returning `true` on success means "this caller
	// is the first emitter".
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_ = f.Close()
		return true
	}
	if os.IsExist(err) {
		// Already emitted in this session — caller should suppress.
		return false
	}
	// Any other I/O failure: err toward emission.
	return true
}
