// Package cache implements <repo>/.browzer/.cache/docs.json — the
// per-machine SHA-256 cache that lets `browzer sync` upload only the
// changed docs since the last successful sync.
package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/browzeremb/browzer-cli/internal/walker"
)

// CacheVersion is the schema version of docs.json.
const CacheVersion = 1

// CachedDoc is the per-file entry stored in DocsCache.
type CachedDoc struct {
	SHA256     string `json:"sha256"`
	DocumentID string `json:"documentId"`
	Size       int64  `json:"size"`
}

// DocsCache is the on-disk structure of .browzer/.cache/docs.json.
type DocsCache struct {
	Version int                  `json:"version"`
	Files   map[string]CachedDoc `json:"files"`
}

// cachePath returns <repo>/.browzer/.cache/docs.json.
func cachePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".browzer", ".cache", "docs.json")
}

// Load reads the cache from disk. Returns an empty cache when the file
// is missing, malformed, or has an unexpected shape — never throws.
// This means a corrupted cache silently triggers a full re-upload on
// the next sync (acceptable: we trade a slow run for never crashing).
func Load(repoRoot string) DocsCache {
	empty := DocsCache{Version: CacheVersion, Files: map[string]CachedDoc{}}
	data, err := os.ReadFile(cachePath(repoRoot))
	if err != nil {
		return empty
	}
	var c DocsCache
	if err := json.Unmarshal(data, &c); err != nil {
		return empty
	}
	if c.Files == nil {
		c.Files = map[string]CachedDoc{}
	}
	if c.Version == 0 {
		c.Version = CacheVersion
	}
	return c
}

// Save writes the cache atomically (tmp file + rename).
func Save(repoRoot string, c DocsCache) error {
	if c.Version == 0 {
		c.Version = CacheVersion
	}
	if c.Files == nil {
		c.Files = map[string]CachedDoc{}
	}
	dir := filepath.Dir(cachePath(repoRoot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := cachePath(repoRoot)
	tmp := final + ".tmp"

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// Diff is the output of comparing the current filesystem walk against
// a stored cache. Added/Modified are uploads; Deleted are server-side
// removals.
type Diff struct {
	Added     []walker.DocFile
	Modified  []walker.DocFile
	Unchanged []walker.DocFile
	Deleted   []DeletedDoc
}

// DeletedDoc identifies a file present in the cache but absent from
// the current walk — it must be removed server-side.
type DeletedDoc struct {
	RelativePath string
	DocumentID   string
}

// DiffDocs compares the current filesystem walk against a stored cache
// and returns added/modified/unchanged/deleted lists. Hash equality
// determines unchanged; presence in cache but not on disk → deleted.
func DiffDocs(current []walker.DocFile, prev DocsCache) Diff {
	d := Diff{}
	seen := make(map[string]struct{}, len(current))
	for _, f := range current {
		seen[f.RelativePath] = struct{}{}
		if existing, ok := prev.Files[f.RelativePath]; ok {
			if existing.SHA256 == f.SHA256 {
				d.Unchanged = append(d.Unchanged, f)
			} else {
				d.Modified = append(d.Modified, f)
			}
			continue
		}
		d.Added = append(d.Added, f)
	}
	for path, entry := range prev.Files {
		if _, ok := seen[path]; ok {
			continue
		}
		d.Deleted = append(d.Deleted, DeletedDoc{
			RelativePath: path,
			DocumentID:   entry.DocumentID,
		})
	}
	return d
}

// ErrNoCache is returned by callers that want to differentiate "cache
// missing" from "cache corrupt" — currently unused but kept for future
// granularity.
var ErrNoCache = errors.New("docs cache missing")
