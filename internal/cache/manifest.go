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
