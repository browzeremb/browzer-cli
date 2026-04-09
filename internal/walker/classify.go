package walker

import (
	"path/filepath"
	"strings"
)

// FileClass is the single source of truth for how a walked file is
// routed through the Browzer ingestion pipeline. Before this existed,
// WalkRepo and WalkDocs each embedded their own inclusion rules, which
// caused Markdown files to appear both as File nodes in the code graph
// AND as Document nodes in the vector index — a duplicate-ingestion
// bug that `workspace index` / `workspace docs` split is meant to fix.
type FileClass int

const (
	// ClassSkip means the file is neither indexable as code structure
	// nor as a document (binaries, unknown extensions, noisy extras).
	// Today this also covers the "anything not classified as code or
	// doc" bucket — the walker still runs its sensitive/ignore/binary
	// checks first, so ClassSkip is the default for anything that
	// survives those filters without being explicitly either class.
	ClassSkip FileClass = iota
	// ClassCode routes the file into the Workspace → Folder → File →
	// Symbol graph via POST /api/workspaces/parse. No embeddings are
	// generated — the server-side parser is a cheap regex extractor.
	ClassCode
	// ClassDoc routes the file into the Document → Chunk vector index
	// via the interactive `browzer workspace docs` picker. Embeddings
	// are generated exactly once per selected file, per submit.
	ClassDoc
)

// docExtensionSet is the authoritative list of "this file should be
// embedded as a document" extensions. Kept lowercase + dot-prefixed to
// match filepath.Ext's output directly.
//
// Intentionally broader than the previous WalkDocs set (.md/.mdx only)
// because `workspace docs` is meant to be the single entry point for
// any prose the user wants indexed — if you add a new document loader
// to packages/core/src/ingestion/, add its extensions here too.
var docExtensionSet = map[string]struct{}{
	".md":  {},
	".mdx": {},
	".pdf": {},
	".txt": {},
	".rst": {},
}

// DocExtensions returns the document extension set as a slice (sorted,
// lowercase, dot-prefixed) for callers that need to display or
// serialize the list (e.g. `browzer workspace docs` --help output).
// The underlying map is intentionally not exported to keep the single
// source of truth immutable at runtime.
func DocExtensions() []string {
	out := make([]string, 0, len(docExtensionSet))
	for ext := range docExtensionSet {
		out = append(out, ext)
	}
	// Sort to keep output deterministic for help text and tests.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// IsDocExtension reports whether the given extension (with or without
// leading dot, any case) is a document extension. Safe helper for
// callers that already have just the extension string.
func IsDocExtension(ext string) bool {
	ext = strings.ToLower(ext)
	if ext != "" && ext[0] != '.' {
		ext = "." + ext
	}
	_, ok := docExtensionSet[ext]
	return ok
}

// ClassifyFile is the single routing decision for a file path,
// post-sensitivity-check. The caller MUST still run IsSensitive,
// .gitignore match, and binary detection before treating the result as
// indexable — ClassifyFile only decides *which bucket* the file
// belongs in, not *whether* to index it at all.
//
// Today the rule is extension-driven: anything in docExtensionSet is
// ClassDoc, anything else is ClassCode. Binaries never reach this
// function in the current walker because IsBinaryFile runs earlier,
// which is why there's no ClassSkip branch here yet. Keep the enum
// anyway — a future "known-noise" list (lockfiles, generated code)
// will want it.
func ClassifyFile(relPath string) FileClass {
	ext := strings.ToLower(filepath.Ext(relPath))
	if _, ok := docExtensionSet[ext]; ok {
		return ClassDoc
	}
	return ClassCode
}
