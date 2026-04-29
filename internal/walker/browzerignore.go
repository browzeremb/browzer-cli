package walker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// negations is a secondary matcher compiled from the bare pattern of every
	// "!<pattern>" line. It is used by ExplicitlyIncludes to detect opt-in
	// overrides of the default-ignore list without relying on go-gitignore's
	// MatchesPathHow semantics (which only returns the last *positive* rule).
	negations *gitignore.GitIgnore
	present   bool
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
		return &BrowzerIgnoreMatcher{
			compiled:  gitignore.CompileIgnoreLines(),
			negations: gitignore.CompileIgnoreLines(),
			present:   false,
		}
	}

	// Parse the file content we already have: split into lines so we can feed
	// both the main compiler and the negations scanner from the same read.
	lines := strings.Split(string(data), "\n")
	compiled := gitignore.CompileIgnoreLines(lines...)

	// Build the negations matcher: strip the leading "!" from every negation
	// line and compile those bare patterns so ExplicitlyIncludes can match them.
	var negLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "!") {
			negLines = append(negLines, trimmed[1:]) // bare pattern without "!"
		}
	}
	negations := gitignore.CompileIgnoreLines(negLines...)

	return &BrowzerIgnoreMatcher{compiled: compiled, negations: negations, present: true}
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

// ExplicitlyIncludes returns true when .browzerignore contains an explicit
// negation rule (e.g. "!CLAUDE.md") whose bare pattern matches relPath. This
// is the escape hatch that lets users re-include files that are blocked by the
// default-ignore list (DefaultIgnorePathSuffixes) without modifying .gitignore.
//
// Returns false when no .browzerignore is present or no negation rule
// matches the path.
func (m *BrowzerIgnoreMatcher) ExplicitlyIncludes(relPath string) bool {
	if !m.present {
		return false
	}
	return m.negations.MatchesPath(relPath)
}
