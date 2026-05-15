package walker

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	gitignore "github.com/sabhiram/go-gitignore"
	"golang.org/x/sync/errgroup"
)

// MaxDepth caps directory recursion. Pathologically deep trees abort.
const MaxDepth = 32

const (
	maxContentLines = 1000
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
// with its first ~1000 lines of content (capped at 4096 chars/line).
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
//   - .browzerignore loaded at root; stacked ON TOP of .gitignore (both
//     must pass). Missing file = zero-op (no paths filtered).
//
// Concurrency: top-level subdirectories of rootPath are walked in
// parallel via a goroutine each (capped at runtime.NumCPU()). Each
// goroutine receives its own cloned ignoreMatcher so nested
// .gitignore patterns it loads while descending stay scoped to its
// subtree and never race with siblings. Top-level regular files are
// handled in the main goroutine. After all subtrees finish, Folders
// and Files are sorted by path so the result is deterministic across
// runs even though the per-subtree work order is not.
func WalkRepo(rootPath string) (*ParseTreeInput, error) {
	rootMatcher, err := loadRootIgnore(rootPath)
	if err != nil {
		return nil, err
	}
	browzerMatcher := LoadBrowzerIgnore(rootPath)
	tree := &ParseTreeInput{RootPath: rootPath}

	entries, err := os.ReadDir(rootPath)
	if err != nil {
		// Unreadable root — return empty tree (mirrors walk's tolerance).
		return tree, nil
	}

	type parallelEntry struct {
		entry os.DirEntry
		name  string
	}
	var (
		parallelDirs   []parallelEntry
		topLevelFiles  []os.DirEntry
	)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		mode := entry.Type()
		if mode&os.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() {
			if _, skip := DefaultIgnoreDirs[name]; skip {
				continue
			}
			if IsDefaultIgnoredPath(name) {
				continue
			}
			if rootMatcher.matches(name + "/") {
				continue
			}
			if browzerMatcher.IsIgnored(name + "/") {
				continue
			}
			parallelDirs = append(parallelDirs, parallelEntry{entry: entry, name: name})
			continue
		}
		if mode.IsRegular() {
			topLevelFiles = append(topLevelFiles, entry)
		}
	}

	// Top-level regular files handled inline — the cost of a top-level
	// file is small and parallelising single files adds no measurable
	// gain.
	for _, entry := range topLevelFiles {
		if pf, ok := processRegularFile(rootPath, entry.Name(), entry, rootMatcher, browzerMatcher); ok {
			tree.Files = append(tree.Files, pf)
		}
	}

	// Fan out top-level subdirectories. Each goroutine writes into its
	// own ParseTreeInput so there is no shared-state contention; the
	// merge happens after errgroup.Wait.
	type subResult struct {
		folders []ParsedFolder
		files   []ParsedFile
	}
	results := make([]subResult, len(parallelDirs))

	workers := runtime.NumCPU()
	if workers > len(parallelDirs) {
		workers = len(parallelDirs)
	}
	if workers < 1 {
		workers = 1
	}

	var g errgroup.Group
	g.SetLimit(workers)

	for i, p := range parallelDirs {
		i, p := i, p
		// Per-goroutine matcher: cloning means nested .gitignore patterns
		// loaded while descending stay scoped to this subtree, which is
		// the semantic guarantee gitignore_stack_test.go pins. The clone
		// is cheap (slice copy + shared compiled-regex pointer) compared
		// to a real mutex on every matches() call from N goroutines.
		subMatcher := rootMatcher.clone()
		g.Go(func() error {
			sub := &ParseTreeInput{RootPath: rootPath}
			sub.Folders = append(sub.Folders, ParsedFolder{Path: p.name, Name: p.name})
			if err := walk(filepath.Join(rootPath, p.name), p.name, subMatcher, browzerMatcher, sub, 1); err != nil {
				return err
			}
			results[i] = subResult{folders: sub.Folders, files: sub.Files}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	for _, r := range results {
		tree.Folders = append(tree.Folders, r.folders...)
		tree.Files = append(tree.Files, r.files...)
	}

	sort.Slice(tree.Folders, func(i, j int) bool { return tree.Folders[i].Path < tree.Folders[j].Path })
	sort.Slice(tree.Files, func(i, j int) bool { return tree.Files[i].Path < tree.Files[j].Path })

	return tree, nil
}

// processRegularFile turns a regular-file DirEntry into a ParsedFile.
// Returns ok=false when the file fails any of the sensitive / ignore /
// binary / read-error checks the walker applies post-stat. Used by both
// WalkRepo's top-level inline pass and walk()'s recursive descent.
func processRegularFile(absDir, relPath string, entry os.DirEntry, matcher *ignoreMatcher, browzerMatcher *BrowzerIgnoreMatcher) (ParsedFile, bool) {
	name := entry.Name()
	if IsSensitive(relPath) {
		return ParsedFile{}, false
	}
	if matcher.matches(relPath) {
		return ParsedFile{}, false
	}
	if browzerMatcher.IsIgnored(relPath) {
		return ParsedFile{}, false
	}
	if ClassifyFile(relPath) == ClassDoc {
		return ParsedFile{}, false
	}
	absPath := filepath.Join(absDir, name)
	if IsBinaryFile(absPath) {
		return ParsedFile{}, false
	}
	info, err := entry.Info()
	if err != nil {
		return ParsedFile{}, false
	}
	content, ok := readFirstLines(absPath, maxContentLines)
	if !ok {
		return ParsedFile{}, false
	}
	return ParsedFile{
		Path:       relPath,
		Name:       name,
		Extension:  filepath.Ext(name),
		SizeBytes:  info.Size(),
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
		Content:    content,
		LineCount:  countLines(absPath),
	}, true
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

func walk(absDir, relDir string, matcher *ignoreMatcher, browzerMatcher *BrowzerIgnoreMatcher, tree *ParseTreeInput, depth int) error {
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
			if browzerMatcher.IsIgnored(relPath + "/") {
				continue
			}
			tree.Folders = append(tree.Folders, ParsedFolder{Path: relPath, Name: name})
			if err := walk(filepath.Join(absDir, name), relPath, matcher, browzerMatcher, tree, depth+1); err != nil {
				return err
			}
			continue
		}

		// Regular files only. Documents (markdown, PDF, ...) are
		// handled by the `workspace docs` flow via WalkDocs — skip them
		// here so the structural code graph doesn't double-index them
		// as both File nodes AND Document nodes.
		if !mode.IsRegular() {
			continue
		}
		if pf, ok := processRegularFile(absDir, relPath, entry, matcher, browzerMatcher); ok {
			tree.Files = append(tree.Files, pf)
		}
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
//
// compiledLen carries the precise "lines reflected in compiled" count
// so matches() only re-builds when the line slice actually grew. This
// drops the redundant recompile that the dirty-flag implementation
// triggered when a nested .gitignore parsed into zero pattern lines
// (blank file, comments-only) — common at apps/<pkg>/.gitignore
// scaffolds.
type ignoreMatcher struct {
	lines       []string
	compiled    *gitignore.GitIgnore
	compiledLen int
}

func newIgnoreMatcher() *ignoreMatcher {
	return &ignoreMatcher{compiled: gitignore.CompileIgnoreLines()}
}

func (m *ignoreMatcher) add(text string) {
	for line := range strings.SplitSeq(text, "\n") {
		m.lines = append(m.lines, strings.TrimRight(line, "\r"))
	}
}

func (m *ignoreMatcher) matches(path string) bool {
	if len(m.lines) != m.compiledLen {
		m.compiled = gitignore.CompileIgnoreLines(m.lines...)
		m.compiledLen = len(m.lines)
	}
	return m.compiled.MatchesPath(path)
}

// clone returns an ignoreMatcher whose pattern list is independent from
// the source. Used by WalkRepo's top-level fan-out so each goroutine can
// accumulate nested .gitignore patterns without racing with siblings.
// The compiled regex pointer is copied by value (go-gitignore's
// *GitIgnore is intended as immutable post-build by the upstream API),
// and the clone's first matches() call rebuilds when its own
// compiledLen drifts from len(lines) — typically once per nested
// .gitignore the subtree encounters.
func (m *ignoreMatcher) clone() *ignoreMatcher {
	return &ignoreMatcher{
		lines:       append([]string(nil), m.lines...),
		compiled:    m.compiled,
		compiledLen: m.compiledLen,
	}
}
