package walker

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitignore "github.com/sabhiram/go-gitignore"
)

// MaxDepth caps directory recursion. Pathologically deep trees abort.
const MaxDepth = 32

const (
	maxContentLines = 100
	maxLineLength   = 4096
)

// ParseTreeInput is the body sent to POST /api/workspaces/parse.
// Field names match the JSON wire format expected by apps/api.
type ParseTreeInput struct {
	RootPath string         `json:"rootPath"`
	Folders  []ParsedFolder `json:"folders"`
	Files    []ParsedFile   `json:"files"`
}

// ParsedFolder is a single directory entry under the walked tree.
type ParsedFolder struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// ParsedFile is a single non-binary, non-sensitive, non-ignored file
// with its first ~100 lines of content (capped at 4096 chars/line).
type ParsedFile struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Extension  string `json:"extension"`
	SizeBytes  int64  `json:"sizeBytes"`
	ModifiedAt string `json:"modifiedAt"`
	Content    string `json:"content"`
	LineCount  int    `json:"lineCount,omitempty"`
}

// WalkRepo walks rootPath and returns a parse-tree input suitable for
// POST /api/workspaces/parse.
//
// Hardening invariants (mirror src/lib/walker.ts):
//   - I-9: symbolic links are skipped at every level. Following them
//     would require a realpath containment check to defend against
//     symlink-to-secret tricks.
//   - I-10: recursion depth capped at MaxDepth (32).
//   - I-11: binary files dropped via IsBinaryFile (probe of first 512 B).
//   - Sensitive files checked BEFORE stat/readFile (IsSensitive).
//   - DefaultIgnoreDirs (node_modules, dist, build, ...) always excluded.
//   - Dot-entries (.git, .next, .cache, ...) always excluded.
//   - .gitignore + .git/info/exclude loaded at root; nested .gitignores
//     re-rooted via RerootGitignore so patterns apply only beneath
//     their containing directory.
func WalkRepo(rootPath string) (*ParseTreeInput, error) {
	matcher, err := loadRootIgnore(rootPath)
	if err != nil {
		return nil, err
	}
	tree := &ParseTreeInput{RootPath: rootPath}
	if err := walk(rootPath, "", matcher, tree, 0); err != nil {
		return nil, err
	}
	return tree, nil
}

// loadRootIgnore reads .gitignore + .git/info/exclude and returns a
// pattern accumulator. Patterns from nested .gitignores are appended in
// walk() as they are discovered.
func loadRootIgnore(rootPath string) (*ignoreMatcher, error) {
	m := newIgnoreMatcher()
	if data, err := os.ReadFile(filepath.Join(rootPath, ".gitignore")); err == nil {
		m.add(string(data))
	}
	if data, err := os.ReadFile(filepath.Join(rootPath, ".git", "info", "exclude")); err == nil {
		m.add(string(data))
	}
	return m, nil
}

func walk(absDir, relDir string, matcher *ignoreMatcher, tree *ParseTreeInput, depth int) error {
	if depth > MaxDepth {
		fmt.Fprintf(os.Stderr, "Warning: max directory depth %d exceeded at %q — stopping recursion.\n", MaxDepth, relDir)
		return nil
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		// Unreadable directory — skip silently (mirrors Node walker).
		return nil
	}

	// Load nested .gitignore (re-rooted to relDir) before processing
	// children so its patterns apply to this directory's entries.
	if relDir != "" {
		if data, err := os.ReadFile(filepath.Join(absDir, ".gitignore")); err == nil {
			matcher.add(RerootGitignore(string(data), relDir))
		}
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Need DirEntry.Type() for symlink check WITHOUT following.
		mode := entry.Type()
		// I-9: symlinks (file OR dir) are skipped entirely.
		if mode&os.ModeSymlink != 0 {
			continue
		}

		var relPath string
		if relDir == "" {
			relPath = name
		} else {
			relPath = relDir + "/" + name
		}

		if entry.IsDir() {
			if _, skip := DefaultIgnoreDirs[name]; skip {
				continue
			}
			if IsDefaultIgnoredPath(relPath) {
				continue
			}
			if matcher.matches(relPath + "/") {
				continue
			}
			tree.Folders = append(tree.Folders, ParsedFolder{Path: relPath, Name: name})
			if err := walk(filepath.Join(absDir, name), relPath, matcher, tree, depth+1); err != nil {
				return err
			}
			continue
		}

		// Regular files only.
		if !mode.IsRegular() {
			continue
		}

		// Sensitive check FIRST — never stat or read sensitive files.
		if IsSensitive(relPath) {
			continue
		}
		if matcher.matches(relPath) {
			continue
		}

		// Documents (markdown, PDF, ...) are handled by the
		// `workspace docs` flow via WalkDocs — skip them here so the
		// structural code graph doesn't double-index them as both
		// File nodes AND Document nodes.
		if ClassifyFile(relPath) == ClassDoc {
			continue
		}

		absPath := filepath.Join(absDir, name)
		// I-11: drop binaries.
		if IsBinaryFile(absPath) {
			continue
		}

		info, err := os.Stat(absPath)
		if err != nil {
			continue
		}

		content, ok := readFirstLines(absPath, maxContentLines)
		if !ok {
			continue
		}

		lineCount := countLines(absPath)

		tree.Files = append(tree.Files, ParsedFile{
			Path:       relPath,
			Name:       name,
			Extension:  filepath.Ext(name),
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
			Content:    content,
			LineCount:  lineCount,
		})
	}
	return nil
}

// readFirstLines reads up to maxLines from absPath, capping each line
// at maxLineLength bytes. Returns the joined text + trailing newline,
// or false on read error.
func readFirstLines(absPath string, maxLines int) (string, bool) {
	f, err := os.Open(absPath)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// Allow long lines so we can detect + truncate them ourselves.
	scanner.Buffer(make([]byte, 0, 1024*64), 1024*1024)

	var sb strings.Builder
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > maxLineLength {
			line = line[:maxLineLength]
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
		count++
		if count >= maxLines {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		// Partial read OK — return what we have.
		return sb.String(), true
	}
	// Original Node behavior: always trailing newline (even when 0 lines).
	if count == 0 {
		return "\n", true
	}
	return sb.String(), true
}

// countLines returns the number of lines in the file at absPath by counting
// newline bytes in the full file content. Returns 0 on any read error.
// This always reflects the actual file size — unlike readFirstLines which
// is capped at maxContentLines.
func countLines(absPath string) int {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return 0
	}
	if len(data) == 0 {
		return 0
	}
	count := bytes.Count(data, []byte("\n")) + 1
	// If the file ends with a newline the last "line" is empty; subtract it
	// to match the conventional definition of line count.
	if data[len(data)-1] == '\n' {
		count--
	}
	return count
}

// ignoreMatcher accumulates .gitignore lines as the walker descends
// the tree. The compiled matcher is rebuilt LAZILY on the first
// matches() call after an add(): the previous implementation
// recompiled inside every add(), which made the cost O(N²) when many
// nested .gitignores were touched in a row before any path was
// matched (a documented gotcha in packages/cli/CLAUDE.md).
//
// Lazy compilation eliminates the redundant recompiles inside a burst
// of add() calls (e.g. root .gitignore + .git/info/exclude both
// loaded before any matches() runs). It does NOT change semantics:
// the underlying go-gitignore matcher still sees the same flat list
// of patterns, so the upstream last-match-wins / negation rules
// continue to apply across nested files — which a per-frame stack
// would silently break, because go-gitignore only flips an
// already-positive match, never re-introduces one.
type ignoreMatcher struct {
	lines    []string
	compiled *gitignore.GitIgnore
	dirty    bool
}

func newIgnoreMatcher() *ignoreMatcher {
	return &ignoreMatcher{compiled: gitignore.CompileIgnoreLines()}
}

func (m *ignoreMatcher) add(text string) {
	for _, line := range strings.Split(text, "\n") {
		m.lines = append(m.lines, strings.TrimRight(line, "\r"))
	}
	// Defer compilation; the next matches() will pick it up.
	m.dirty = true
}

func (m *ignoreMatcher) matches(path string) bool {
	if m.dirty {
		m.compiled = gitignore.CompileIgnoreLines(m.lines...)
		m.dirty = false
	}
	return m.compiled.MatchesPath(path)
}
