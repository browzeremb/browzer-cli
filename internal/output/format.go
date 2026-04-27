package output

import (
	"fmt"
	"strings"
)

// ExploreEntry mirrors the JSON entry returned by GET
// /api/workspaces/:id/explore. Field names match the wire format.
//
// Anchor is a stable, unique snippet line (40–80 chars) the consumer
// can `grep` against to relocate this entry after the indexed snapshot
// drifts from working-tree HEAD. Line numbers are inherently fragile
// across edits — anchors survive most surrounding-line churn. Computed
// CLI-side from Snippet at result-mapping time; falls back to Name on
// snippet-less folder/symbol entries.
type ExploreEntry struct {
	Path       string   `json:"path"`
	Type       string   `json:"type"` // file | folder | symbol
	Name       string   `json:"name,omitempty"`
	Anchor     string   `json:"anchor,omitempty"`
	LineRange  string   `json:"lineRange,omitempty"`
	Snippet    string   `json:"snippet,omitempty"`
	Score      float64  `json:"score"`
	Exports    []string `json:"exports,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	ImportedBy []string `json:"importedBy,omitempty"`
	Lines      int      `json:"lines,omitempty"`
}

// ExtractAnchor returns a stable per-entry anchor string usable by
// downstream skills as a grep target. It picks the first non-trivial
// line of the snippet — skipping blanks, comment-only lines, and very
// short lines that wouldn't be unique. Caps at 80 chars. Falls back to
// `name` when no qualifying line exists. Always trimmed of trailing
// whitespace; never returns a leading/trailing comment marker that
// would prevent a literal grep from matching the source.
func ExtractAnchor(snippet, name string) string {
	if snippet == "" {
		return name
	}
	for line := range strings.SplitSeq(snippet, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		stripped := strings.TrimLeft(trimmed, " \t")
		if len(stripped) < 12 {
			continue
		}
		// Skip lines that are purely a comment-only marker — they rot
		// independently of the surrounding code and rarely serve as
		// reliable anchors.
		switch {
		case strings.HasPrefix(stripped, "//"),
			strings.HasPrefix(stripped, "/*"),
			strings.HasPrefix(stripped, "* "),
			strings.HasPrefix(stripped, "*/"),
			strings.HasPrefix(stripped, "--"),
			strings.HasPrefix(stripped, "#") && !strings.HasPrefix(stripped, "#!"):
			continue
		}
		if len(stripped) > 80 {
			stripped = stripped[:80]
		}
		return stripped
	}
	return name
}

// DepsResult mirrors the JSON returned by GET /api/workspaces/:id/deps.
type DepsResult struct {
	Path       string   `json:"path"`
	Exports    []string `json:"exports,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	ImportedBy []string `json:"importedBy,omitempty"`
}

// SearchResult mirrors the JSON entry returned by GET
// /api/workspaces/:id/search.
type SearchResult struct {
	Text         string  `json:"text"`
	Position     int     `json:"position"`
	Score        float64 `json:"score"`
	DocumentName string  `json:"documentName"`
}

// FormatExploreResults renders the human-readable form of an explore
// payload (used when neither --json nor --save is set).
func FormatExploreResults(entries []ExploreEntry) string {
	if len(entries) == 0 {
		return "No matches.\n"
	}
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s", e.Path)
		if e.LineRange != "" {
			fmt.Fprintf(&sb, ":%s", e.LineRange)
		}
		fmt.Fprintf(&sb, " [%s] score=%.3f", e.Type, e.Score)
		if e.Lines > 0 {
			fmt.Fprintf(&sb, " lines=%d", e.Lines)
		}
		sb.WriteString("\n")
		if len(e.Exports) > 0 {
			fmt.Fprintf(&sb, "  exports: %s\n", strings.Join(e.Exports, ", "))
		}
		if len(e.Imports) > 0 {
			fmt.Fprintf(&sb, "  imports: %s\n", strings.Join(e.Imports, ", "))
		}
		if len(e.ImportedBy) > 0 {
			fmt.Fprintf(&sb, "  importedBy: %s\n", strings.Join(e.ImportedBy, ", "))
		}
		if e.Snippet != "" {
			for line := range strings.SplitSeq(strings.TrimRight(e.Snippet, "\n"), "\n") {
				fmt.Fprintf(&sb, "  %s\n", line)
			}
		}
	}
	return sb.String()
}

// FormatDepsResults renders the human-readable form of a deps response.
func FormatDepsResults(resp DepsResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", resp.Path)
	if len(resp.Exports) > 0 {
		fmt.Fprintf(&sb, "  Exports (%d): %s\n", len(resp.Exports), strings.Join(resp.Exports, ", "))
	}
	if len(resp.Imports) > 0 {
		fmt.Fprintf(&sb, "  Imports (%d):\n", len(resp.Imports))
		for _, imp := range resp.Imports {
			fmt.Fprintf(&sb, "    %s\n", imp)
		}
	}
	if len(resp.ImportedBy) > 0 {
		fmt.Fprintf(&sb, "  Imported by (%d):\n", len(resp.ImportedBy))
		for _, ib := range resp.ImportedBy {
			fmt.Fprintf(&sb, "    %s\n", ib)
		}
	}
	return sb.String()
}

// FormatSearchResults renders the human form of vector search hits.
func FormatSearchResults(results []SearchResult) string {
	if len(results) == 0 {
		return "No matches.\n"
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "%s score=%.3f\n", r.DocumentName, r.Score)
		fmt.Fprintf(&sb, "  %s\n", strings.TrimSpace(r.Text))
	}
	return sb.String()
}

// MentionItem mirrors one entry in the POST /api/workspaces/:id/mentions response.
type MentionItem struct {
	Doc            string   `json:"doc"`
	ChunkCount     int      `json:"chunkCount"`
	SampleEntities []string `json:"sampleEntities,omitempty"`
}

// MentionsMeta describes the indexed-snapshot context for a mentions
// response. It lets callers tell "file is outside the indexed snapshot"
// (FileIndexed=false) apart from "file is in the snapshot but no docs
// reference it" (FileIndexed=true, Mentions empty/null) — a distinction
// `update-docs` Phase 1a depends on to decide whether to fall back to a
// grep over docs/ or to skip propagation entirely.
type MentionsMeta struct {
	IndexedCommit string `json:"indexedCommit,omitempty"`
	WorkingCommit string `json:"workingCommit,omitempty"`
	CommitsBehind int    `json:"commitsBehind"`
	FileIndexed   bool   `json:"fileIndexed"`
	Stale         bool   `json:"stale"`
}

// MentionsResult mirrors the JSON returned by POST /api/workspaces/:id/mentions.
//
// Meta is populated CLI-side from the local git working tree + the
// resolved path's presence in the indexed snapshot — the API itself
// does not return staleness data. Existing callers reading only
// `.mentions` are unaffected by the new field.
type MentionsResult struct {
	Path        string        `json:"path"`
	WorkspaceID string        `json:"workspaceId"`
	Meta        MentionsMeta  `json:"meta"`
	Mentions    []MentionItem `json:"mentions"`
}

// FormatMentionsResults renders the human-readable form of a mentions response.
func FormatMentionsResults(resp MentionsResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s → %d docs\n", resp.Path, len(resp.Mentions))
	for _, m := range resp.Mentions {
		entities := ""
		if len(m.SampleEntities) > 0 {
			entities = " [" + strings.Join(m.SampleEntities, ", ") + "]"
		}
		fmt.Fprintf(&sb, "  %-60s %d chunks%s\n", m.Doc, m.ChunkCount, entities)
	}
	return sb.String()
}

// FormatStalenessWarning is the stderr-bound warning emitted before
// search/explore when the local HEAD is ahead of lastSyncCommit.
func FormatStalenessWarning(commitsBehind int) string {
	return fmt.Sprintf(
		"⚠ Index %d commits behind. Run `browzer sync`.\n",
		commitsBehind,
	)
}
