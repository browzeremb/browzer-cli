package hooks

import (
	"path/filepath"
	"regexp"
	"strings"
)

// PathClass enumerates the buckets returned by ClassifyPath.
type PathClass string

const (
	// ClassNeverRewrite means the path is on the never-rewrite list:
	// tiny config / infra files demanding exact-text edits where the
	// daemon Read/Bash path-swap would break the Edit harness.
	ClassNeverRewrite PathClass = "never-rewrite"

	// ClassConfigSurface means the path lives under a config / docs /
	// out-of-index surface (e.g. .claude, dist, node_modules, .next).
	ClassConfigSurface PathClass = "config-surface"

	// ClassCode means the path looks like source code.
	ClassCode PathClass = "code"

	// ClassDoc means the path is a non-code documentation artefact
	// (.md, .mdx, .json, .yaml, .yml, .toml, .lock, .env).
	ClassDoc PathClass = "doc"

	// ClassUnknown is the fall-through when the path has no extension
	// and matches none of the above.
	ClassUnknown PathClass = "unknown"
)

// NeverRewriteRE matches file paths whose owners must never be force-
// rewritten via the Read/Bash daemon path-swap. Mirrors the JS source
// at packages/skills/hooks/guards/_util.mjs (NEVER_REWRITE_RE).
//
// Covers:
//   - bare names: Dockerfile, drizzle.config.ts, tsup.config.ts
//   - extensions: .sql, .toml, .env, .env.<suffix>, .ya?ml
//   - root files: package.json, CLAUDE.md, AGENTS.md
//   - any path component opening with .env (e.g. .env.local)
//
// Go's `regexp` package is RE2 — no lookarounds, no `(?i)` inline-flags
// at non-leading positions. The trailing `(?i)` from the JS source is
// expressed via a leading `(?i)` here.
var NeverRewriteRE = regexp.MustCompile(`(?i)(^|/)(Dockerfile|drizzle\.config\.ts|tsup\.config\.ts)$|\.(sql|toml|env(\.[a-z]+)?|ya?ml)$|(^|/)package\.json$|(^|/)CLAUDE\.md$|(^|/)AGENTS\.md$|(^|/)\.env(\.|$)`)

// ConfigSurfaceRE matches paths under known config / infra / docs
// directories OR with non-code extensions. Mirrors the JS source at
// packages/skills/hooks/guards/_util.mjs (CONFIG_SURFACE_RE).
//
// Two alternatives:
//  1. Directory-component match against the known set of config dirs.
//  2. Trailing extension match against common non-code extensions —
//     guarded with a synthetic "non-word-char or EOL" boundary
//     simulating the JS negative-lookahead `(?![a-z0-9])` so that
//     `.envs` does NOT match while `.env` and `.env.local` do.
//
// Path-component classification is more precise than substring sniffing.
// The fall-through `MatchString` below handles the extension case after
// the dir-component check.
var ConfigSurfaceRE = regexp.MustCompile(`(?i)(^|[\s/*])(\.claude|\.github|\.vscode|\.husky|\.changeset|node_modules|dist|build|coverage|\.turbo|\.next)([/\s]|$)`)

// configSurfaceExtRE captures the non-code extension alternative from
// CONFIG_SURFACE_RE — split out so we can apply the RE2-friendly
// "boundary" simulation manually after the regexp match.
var configSurfaceExtRE = regexp.MustCompile(`(?i)\.(json|ya?ml|toml|md|mdx|lock|env|gitignore|editorconfig|prettierrc|eslintrc)$`)

// nonCodeExts mirrors the NON_CODE_EXT set in the JS source
// (_util.mjs:251-260). Used by ClassifyPath to bucket doc/data
// files into ClassDoc.
var nonCodeExts = map[string]struct{}{
	".md":   {},
	".mdx":  {},
	".json": {},
	".yaml": {},
	".yml":  {},
	".toml": {},
	".lock": {},
	".env":  {},
}

// ConfigSurfaceTarget builds the target string that ConfigSurfaceRE is
// matched against. It joins non-empty parts with a single space separator,
// mirroring the inline builder that previously appeared independently in
// each of the four hook guards (postuse_grep, postuse_glob, block_glob,
// suggest_grep). Co-located here because ConfigSurfaceRE is the sole reason
// the target string exists.
func ConfigSurfaceTarget(parts ...string) string {
	var b strings.Builder
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !first {
			b.WriteByte(' ')
		}
		b.WriteString(p)
		first = false
	}
	return b.String()
}

// ClassifyPath buckets a path into one of {never-rewrite, config-surface,
// code, doc, unknown}. Precedence runs top-down:
//
//  1. NeverRewriteRE wins outright (these files are tiny + edit-critical).
//  2. ConfigSurfaceRE catches anything under a known config / infra dir,
//     or with a recognised non-code extension.
//  3. Extension lookup against nonCodeExts → doc bucket.
//  4. Anything with an extension that didn't match the doc set → code.
//  5. Extensionless paths fall through to unknown.
func ClassifyPath(p string) PathClass {
	if p == "" {
		return ClassUnknown
	}

	// Normalise OS-specific separators so the regexes (anchored on `/`)
	// still bind on Windows-style input. Hook input arrives from
	// Claude Code's JSON harness which already uses POSIX separators,
	// but defending against either is cheap.
	norm := strings.ReplaceAll(p, `\`, `/`)

	if NeverRewriteRE.MatchString(norm) {
		return ClassNeverRewrite
	}

	if ConfigSurfaceRE.MatchString(norm) || configSurfaceExtRE.MatchString(norm) {
		return ClassConfigSurface
	}

	ext := strings.ToLower(filepath.Ext(norm))
	if ext != "" {
		if _, ok := nonCodeExts[ext]; ok {
			return ClassDoc
		}
		return ClassCode
	}
	return ClassUnknown
}
