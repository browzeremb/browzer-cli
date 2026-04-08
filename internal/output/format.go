package output

import (
	"fmt"
	"strings"
)

// ExploreEntry mirrors the JSON entry returned by GET
// /api/workspaces/:id/explore. Field names match the wire format.
type ExploreEntry struct {
	Path      string `json:"path"`
	Type      string `json:"type"` // file | folder | symbol
	Name      string `json:"name"`
	LineRange string `json:"lineRange,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
	Score     float64 `json:"score"`
}

// SearchResult mirrors the JSON entry returned by GET
// /api/workspaces/:id/search.
type SearchResult struct {
	Text         string  `json:"text"`
	Position     int     `json:"position"`
	Score        float64 `json:"score"`
	DocumentName string  `json:"documentName"`
}

// WorkspaceSummary is the row format used by `workspace list`.
type WorkspaceSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RootPath     string `json:"rootPath"`
	FileCount    int    `json:"fileCount"`
	FolderCount  int    `json:"folderCount"`
	SymbolCount  int    `json:"symbolCount"`
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
		fmt.Fprintf(&sb, " [%s] %s score=%.3f\n", e.Type, e.Name, e.Score)
		if e.Snippet != "" {
			for _, line := range strings.Split(strings.TrimRight(e.Snippet, "\n"), "\n") {
				fmt.Fprintf(&sb, "  %s\n", line)
			}
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

// FormatWorkspaceList renders the human form of `workspace list`.
func FormatWorkspaceList(ws []WorkspaceSummary) string {
	if len(ws) == 0 {
		return "No workspaces.\n"
	}
	var sb strings.Builder
	for _, w := range ws {
		fmt.Fprintf(&sb, "%s  %s  files=%d folders=%d symbols=%d\n",
			w.ID, w.Name, w.FileCount, w.FolderCount, w.SymbolCount)
		if w.RootPath != "" {
			fmt.Fprintf(&sb, "  %s\n", w.RootPath)
		}
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
