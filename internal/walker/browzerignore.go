package walker

import (
	"fmt"
	"os"
	"path/filepath"

	gitignore "github.com/sabhiram/go-gitignore"
)

// BrowzerIgnoreMatcher wraps go-gitignore so .browzerignore can be stacked
// ON TOP of .gitignore. An empty / missing .browzerignore yields a matcher
// whose IsIgnored always returns false (zero-op stack).
//
// Grammar is identical to .gitignore: blank lines and lines beginning with
// '#' are ignored; negation via '!' is supported; last-match-wins semantics
// apply. This is not a replacement for the .gitignore filter — the two
// matchers are ANDed together by the walker.
type BrowzerIgnoreMatcher struct {
	compiled *gitignore.GitIgnore
	present  bool
}

// LoadBrowzerIgnore reads <rootPath>/.browzerignore. Missing file returns a
// non-nil always-false matcher so callers never need nil guards. A file that
// cannot be parsed (e.g. IO error after open) emits a warning to stderr and
// falls back to the same always-false matcher, preserving the tolerant pattern
// used by the rest of the walker package.
func LoadBrowzerIgnore(rootPath string) *BrowzerIgnoreMatcher {
	path := filepath.Join(rootPath, ".browzerignore")
	data, err := os.ReadFile(path)
	if err != nil {
		// Missing file is expected; any other error is unexpected but non-fatal.
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not read .browzerignore: %v — ignoring.\n", err)
		}
		return &BrowzerIgnoreMatcher{compiled: gitignore.CompileIgnoreLines(), present: false}
	}

	// go-gitignore silently tolerates invalid patterns, so we just compile.
	_ = data // ReadFile succeeded; use the path-based API for correct line-split.
	compiled, compErr := gitignore.CompileIgnoreFile(path)
	if compErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not parse .browzerignore: %v — ignoring.\n", compErr)
		return &BrowzerIgnoreMatcher{compiled: gitignore.CompileIgnoreLines(), present: false}
	}
	return &BrowzerIgnoreMatcher{compiled: compiled, present: true}
}

// IsIgnored reports whether the forward-slash relative path is excluded by
// .browzerignore. Mirrors go-gitignore's MatchesPath semantics: last-match-wins,
// negations supported. Returns false when no .browzerignore file was found.
func (m *BrowzerIgnoreMatcher) IsIgnored(relPath string) bool {
	if !m.present {
		return false
	}
	return m.compiled.MatchesPath(relPath)
}
