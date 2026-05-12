// Package session manages per-session state for the CLI to avoid re-emitting
// large context blocks (like the CLAUDE.md invariants section) when nothing
// has changed across multiple get-step calls within the same shell session.
//
// Session identity is determined by:
//  1. The env var BROWZER_SESSION_ID when set.
//  2. The parent process ID (PPID) as a stable-within-shell fallback.
//
// The invariants hash cache lives at:
//
//	~/.browzer/session/<sid>.invariants.sha256
//
// On each get-step call the caller:
//   1. Computes the SHA-256 of the invariants block text.
//   2. Calls ShouldEmitFull to decide whether to emit the full block or just
//      the "[invariants unchanged — sha256:<hex>]" marker.
//   3. Calls RecordHash after emitting (succeeds or is a no-op on error).
//
// The cache is per-session and per-feature-slug to avoid cross-feature
// collisions within the same shell session.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InvariantsMode controls whether the full invariants block is emitted.
type InvariantsMode string

const (
	// InvariantsModeAuto emits the full block on first call in a session,
	// hash-only marker on subsequent calls unless the hash changed.
	InvariantsModeAuto InvariantsMode = "auto"
	// InvariantsModeHash always emits only the hash marker (never full block).
	InvariantsModeHash InvariantsMode = "hash"
	// InvariantsModeFull always emits the full block.
	InvariantsModeFull InvariantsMode = "full"
	// InvariantsModeSkip suppresses invariants entirely (no marker, no block).
	InvariantsModeSkip InvariantsMode = "skip"
)

// ParseInvariantsMode parses a user-supplied mode flag value. Defaults to
// InvariantsModeAuto on unrecognised input (preserving backward compatibility).
func ParseInvariantsMode(s string) InvariantsMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hash":
		return InvariantsModeHash
	case "full":
		return InvariantsModeFull
	case "skip":
		return InvariantsModeSkip
	default:
		return InvariantsModeAuto
	}
}

// HashInvariants computes the SHA-256 hex digest of the invariants block text.
// The hash is stable for identical input across runs and processes.
func HashInvariants(invariantsBlock string) string {
	h := sha256.Sum256([]byte(invariantsBlock))
	return hex.EncodeToString(h[:])
}

// SessionID returns the current session ID. Resolution order:
//  1. BROWZER_SESSION_ID env var.
//  2. Parent process ID (strconv.Itoa(os.Getppid())).
//
// The ID is sanitised to only alphanumeric + dash/underscore/dot to make it
// safe as a filename component.
func SessionID() string {
	if id := os.Getenv("BROWZER_SESSION_ID"); id != "" {
		return sanitiseSessionID(id)
	}
	return fmt.Sprintf("ppid-%d", os.Getppid())
}

// sanitiseSessionID strips characters unsafe for filesystem paths.
func sanitiseSessionID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// sessionCachePath returns the path where the invariants hash is cached for
// the given session ID. Uses BROWZER_HOME when set, otherwise ~/.browzer/.
func sessionCachePath(sid string) string {
	home := os.Getenv("BROWZER_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".browzer")
	}
	return filepath.Join(home, "session", sid+".invariants.sha256")
}

// readCachedHash reads the stored hash for sid. Returns "" when the cache
// file is absent or unreadable.
func readCachedHash(sid string) string {
	p := sessionCachePath(sid)
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// RecordHash writes the current invariants hash to the session cache file.
// Creates parent directories as needed. Errors are silently dropped — this is
// an observability helper; the caller should not fail on cache write errors.
func RecordHash(sid, hash string) {
	p := sessionCachePath(sid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(hash+"\n"), 0o600)
}

// ShouldEmitFull reports whether the full invariants block should be emitted
// for the given session, mode, and current hash.
//
// Decision table:
//   - mode=full  → always true
//   - mode=skip  → always false
//   - mode=hash  → always false (emit marker only)
//   - mode=auto  → true when cache is empty OR cached hash differs from current
func ShouldEmitFull(sid, currentHash string, mode InvariantsMode) bool {
	switch mode {
	case InvariantsModeFull:
		return true
	case InvariantsModeSkip, InvariantsModeHash:
		return false
	default: // InvariantsModeAuto
		cached := readCachedHash(sid)
		return cached == "" || cached != currentHash
	}
}

// HashMarker returns the "[invariants unchanged — sha256:<hex>]" string that
// replaces the full block when ShouldEmitFull returns false.
func HashMarker(hash string) string {
	return fmt.Sprintf("[invariants unchanged — sha256:%s]", hash)
}
