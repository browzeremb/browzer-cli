// Package walker traverses the repository under `init`/`sync`,
// honoring .gitignore + the hardcoded sensitive-file blocklist + the
// shared baseline of always-skipped directories.
//
// The sensitive blocklist is hardcoded and cannot be overridden by user
// config — it must always be checked BEFORE any stat/readFile syscall
// so sensitive files never touch disk metadata. Mirrors
// packages/shared/src/sensitive-filter.ts.
package walker

import (
	"path/filepath"
	"regexp"
	"strings"
)

// sensitivePathPatterns match against the normalized (forward-slash)
// full relative path. Mirrors SENSITIVE_PATH_PATTERNS in
// packages/shared/src/sensitive-filter.ts byte-for-byte.
var sensitivePathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|/)\.env(?:\..+)?$`),
	regexp.MustCompile(`\.(?:pem|key|cert|p12|pfx|jks|keystore)$`),
	regexp.MustCompile(`(?:^|/)\.(?:ssh|gnupg|gcloud)/`),
	regexp.MustCompile(`(?:^|/)\.aws/credentials$`),
	regexp.MustCompile(`(?:^|/)id_(?:rsa|ed25519|dsa)(?:\.pub)?$`),
	regexp.MustCompile(`(?:^|/)\.npmrc$`),
	regexp.MustCompile(`(?:^|/)\.pypirc$`),
	regexp.MustCompile(`\.(?:sqlite|db)$`),
}

// sensitiveNamePatterns match against the basename only. Go's regexp
// engine (RE2) does NOT support look-behind/look-ahead, so we emulate
// the original "letter delimiter" semantics manually in matchName().
//
// Original JS:
//
//	(?<![a-zA-Z])credentials?(?![a-zA-Z])
//
// Cases the original wanted to capture:
//
//	"credentials.json"  → true  (start + dot delimiter)
//	"api_token.json"    → true  (underscore before token is non-letter)
//	"aws-secret.txt"    → true  (hyphen delimiter)
//
// Cases the original rejected:
//
//	"tokenizer.ts"      → false (i immediately follows token)
//	"secretary.ts"      → false (a immediately follows secret)
var sensitiveKeywords = []string{"credential", "secret", "token"}

// IsSensitive returns true if filePath matches the hardcoded blocklist
// of sensitive files. The check is on the normalized (forward-slash)
// path; Windows separators are converted in-place.
//
// MUST be called BEFORE any stat/readFile syscall to avoid touching
// sensitive file metadata.
func IsSensitive(filePath string) bool {
	// We normalize separators in two steps. filepath.ToSlash handles
	// the platform separator (a no-op on POSIX), and the explicit
	// ReplaceAll catches literal backslashes that may have come from
	// a string built on a different OS — common when paths are
	// shipped across the wire from Windows clients.
	normalized := strings.ReplaceAll(filepath.ToSlash(filePath), `\`, "/")
	for _, p := range sensitivePathPatterns {
		if p.MatchString(normalized) {
			return true
		}
	}
	name := filepath.Base(normalized)
	return matchSensitiveName(name)
}

// matchSensitiveName implements the JS look-behind/look-ahead
// "delimited keyword" semantics that RE2 cannot express directly.
//
// A keyword (credential, secret, token) matches if every position
// where it appears in the name has a non-letter byte immediately
// before AND immediately after the keyword body. The trailing "s" is
// optional (matching `credentials?` etc).
func matchSensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range sensitiveKeywords {
		// Try base keyword and plural form.
		for _, suffix := range []string{"", "s"} {
			needle := kw + suffix
			start := 0
			for {
				idx := strings.Index(lower[start:], needle)
				if idx < 0 {
					break
				}
				abs := start + idx
				before := byte(0)
				if abs > 0 {
					before = lower[abs-1]
				}
				after := byte(0)
				end := abs + len(needle)
				if end < len(lower) {
					after = lower[end]
				}
				if !isLetter(before) && !isLetter(after) {
					return true
				}
				start = abs + 1
			}
		}
	}
	return false
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// DefaultIgnoreDirs mirrors DEFAULT_IGNORE in
// packages/shared/src/ignore-patterns.ts. Dot-directories (.git, .next,
// etc.) are already excluded by the walker's dot-prefix guard, so only
// non-dot entries live here.
var DefaultIgnoreDirs = map[string]struct{}{
	"node_modules": {},
	"dist":         {},
	"build":        {},
	"__pycache__":  {},
	"coverage":     {},
	"venv":         {},
	"env":          {},
	// Claude Code agent skill packages — markdown-only, no source-code
	// signal worth indexing, and frequently large enough to dominate
	// the chunk budget on monorepos that vendor a `skills/` directory.
	"skills": {},
}

// DefaultIgnorePathSuffixes holds multi-component path patterns AND
// single-filename patterns that should be skipped by default. Checked via
// path suffix match — `normalized == suffix` covers root-level matches and
// `strings.HasSuffix(normalized, "/"+suffix)` covers nested matches. So
// "CLAUDE.md" matches both `./CLAUDE.md` and `apps/web/CLAUDE.md`.
//
// CLAUDE.md is default-ignored because the file is a Claude Code
// agent-context manifest — it carries no source-code or first-party
// documentation signal worth indexing in the workspace, and historically
// produced rogue rows on `/dashboard/documents` SOURCE DOCUMENTS even on
// sandboxes that did not check the file in (dogfood F-15 / DOG-CLI-1,
// 2026-04-29). Operators who DO want CLAUDE.md indexed can override via
// `.browzerignore` (whitelist with `!CLAUDE.md`) or pass an explicit
// `browzer workspace docs --add CLAUDE.md`.
var DefaultIgnorePathSuffixes = []string{
	"test/fixtures",
	"CLAUDE.md",
	"AGENTS.md",
}

// IsDefaultIgnoredPath returns true if relPath matches any hardcoded
// default-ignore path patterns (e.g., test/fixtures). relPath should use
// forward slashes as separators (call filepath.ToSlash first if needed).
func IsDefaultIgnoredPath(relPath string) bool {
	normalized := strings.ReplaceAll(filepath.ToSlash(relPath), `\`, "/")
	for _, suffix := range DefaultIgnorePathSuffixes {
		if normalized == suffix || strings.HasSuffix(normalized, "/"+suffix) {
			return true
		}
	}
	return false
}
