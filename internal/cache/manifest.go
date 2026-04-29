// Package cache — WorkspaceManifest tracks known workspaces between sync
// runs for bidirectional reconciliation (AC-5-cli).
//
// The manifest is stored as a per-org JSON file inside the repo's
// .browzer/.cache/ directory (same base as docs.json), keyed by orgId.
// Each entry holds the last-known server state so the sync reconciler can
// detect server-side renames/deletes and apply last-writer-wins semantics.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ManifestEntry is one workspace entry tracked locally between syncs.
type ManifestEntry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RootPath  string    `json:"rootPath"`
	UpdatedAt time.Time `json:"updatedAt"`
	// LocallyModified is set to true when the local entry has been changed
	// (renamed or flagged for deletion) and the change has not yet been pushed
	// to the server. ReconcileWorkspaceManifest reads this flag to decide
	// whether to call UpdateWorkspace or DeleteWorkspace on the next sync.
	LocallyModified bool `json:"locallyModified,omitempty"`
	// PendingDelete, when true, means the workspace should be deleted from the
	// server on the next reconciliation push pass.
	PendingDelete bool `json:"pendingDelete,omitempty"`
}

// WorkspaceManifest is the in-memory representation of the per-org workspace
// manifest. It maps workspace ID → ManifestEntry.
type WorkspaceManifest struct {
	entries map[string]ManifestEntry
}

// manifestFilePath returns the path to the org-scoped manifest file:
// <cachePath>/workspaces-<orgId>.json
func manifestFilePath(cachePath, orgID string) string {
	return filepath.Join(cachePath, "workspaces-"+orgID+".json")
}

// LoadWorkspaceManifest reads the manifest for the given orgId from cachePath.
// Returns an empty (non-nil) manifest when the file is missing or malformed —
// callers can always write to the returned manifest without a nil-guard.
func LoadWorkspaceManifest(cachePath, orgID string) (*WorkspaceManifest, error) {
	m := &WorkspaceManifest{entries: make(map[string]ManifestEntry)}

	data, err := os.ReadFile(manifestFilePath(cachePath, orgID))
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, fmt.Errorf("read workspace manifest: %w", err)
	}

	var raw []ManifestEntry
	if jsonErr := json.Unmarshal(data, &raw); jsonErr != nil {
		// Corrupt file — return empty manifest so the next sync rebuilds it.
		return m, nil
	}
	for _, e := range raw {
		m.entries[e.ID] = e
	}
	return m, nil
}

// Save writes the manifest atomically (tmp + rename) to cachePath/workspaces-<orgId>.json.
func (m *WorkspaceManifest) Save(cachePath, orgID string) error {
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}

	raw := make([]ManifestEntry, 0, len(m.entries))
	for _, e := range m.entries {
		raw = append(raw, e)
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace manifest: %w", err)
	}
	data = append(data, '\n')

	final := manifestFilePath(cachePath, orgID)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write workspace manifest tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("rename workspace manifest: %w", err)
	}
	return nil
}

// Get returns the ManifestEntry for the given workspace ID, and whether it exists.
func (m *WorkspaceManifest) Get(id string) (ManifestEntry, bool) {
	e, ok := m.entries[id]
	return e, ok
}

// Upsert inserts or replaces the entry for entry.ID.
func (m *WorkspaceManifest) Upsert(entry ManifestEntry) {
	m.entries[entry.ID] = entry
}

// Remove deletes the entry for the given workspace ID. No-op if absent.
func (m *WorkspaceManifest) Remove(id string) {
	delete(m.entries, id)
}

// All returns a snapshot of all entries in the manifest (order unspecified).
func (m *WorkspaceManifest) All() []ManifestEntry {
	out := make([]ManifestEntry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	return out
}

// ── Per-feature cache metadata ────────────────────────────────────────────────

// FeatureCacheEntry tracks the pre-warm state for a single feature's
// .browzer-cache/ directory. It is keyed by featureId in WorkspaceManifest.FeatureCache.
//
// Cache invalidation: compare WarmedAtSHA against the current `git rev-parse HEAD`.
// When they differ the cache is stale and the orchestrator should re-run Step 2.5.
//
// Cache layout (runtime dirs are created by the orchestrator; documented here):
//
//	$CacheDir/
//	  deps/<file-slug>.json       # output of `browzer deps <path> --save`
//	  rdeps/<file-slug>.json      # output of `browzer deps <path> --reverse --save`
//	  mentions/<file-slug>.json   # output of `browzer mentions <path> --save`
//
// <file-slug> = path with / replaced by _ (mirrors fileSlug in internal/workflow/query.go).
type FeatureCacheEntry struct {
	FeatureID     string    `json:"featureId"`
	WarmedAt      time.Time `json:"warmedAt"`
	WarmedAtSHA   string    `json:"warmedAtSHA"`   // git rev-parse HEAD when warming started
	CacheDir      string    `json:"cacheDir"`       // absolute path to .browzer-cache/ for the feature
	DepsFiles     []string  `json:"depsFiles"`     // source files with deps cached
	MentionsFiles []string  `json:"mentionsFiles"` // source files with mentions cached
}

// featureManifestFilePath returns the path to the shared feature-cache manifest:
// <cachePath>/feature-cache.json
func featureManifestFilePath(cachePath string) string {
	return filepath.Join(cachePath, "feature-cache.json")
}

// LoadFeatureCache reads the feature-cache manifest from cachePath.
// Returns an empty (non-nil) map when the file is missing or malformed.
func LoadFeatureCache(cachePath string) (map[string]FeatureCacheEntry, error) {
	out := make(map[string]FeatureCacheEntry)

	data, err := os.ReadFile(featureManifestFilePath(cachePath))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("read feature-cache manifest: %w", err)
	}

	var raw []FeatureCacheEntry
	if jsonErr := json.Unmarshal(data, &raw); jsonErr != nil {
		// Corrupt file — return empty map so the next sync rebuilds it.
		return out, nil
	}
	for _, e := range raw {
		out[e.FeatureID] = e
	}
	return out, nil
}

// SaveFeatureCache writes the feature-cache manifest atomically to
// cachePath/feature-cache.json.
func SaveFeatureCache(cachePath string, cache map[string]FeatureCacheEntry) error {
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return fmt.Errorf("create feature-cache dir: %w", err)
	}

	raw := make([]FeatureCacheEntry, 0, len(cache))
	for _, e := range cache {
		raw = append(raw, e)
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal feature-cache: %w", err)
	}
	data = append(data, '\n')

	final := featureManifestFilePath(cachePath)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write feature-cache tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("rename feature-cache: %w", err)
	}
	return nil
}

// IsCacheStale returns true when the feature's cached SHA differs from
// currentSHA or when no cache entry exists for featureID.
//
// Per Risk Checkpoint #7 from the Phase 5 plan: the orchestrator MUST call
// IsCacheStale before consuming any cached file. A stale cache (HEAD moved
// mid-feature) means blast-radius data may no longer reflect the live tree.
func IsCacheStale(cache map[string]FeatureCacheEntry, featureID, currentSHA string) bool {
	entry, ok := cache[featureID]
	if !ok {
		return true
	}
	return entry.WarmedAtSHA != currentSHA
}
