package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ManifestSymbol mirrors WorkspaceManifestSymbol from packages/core (TS).
type ManifestSymbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Signature string `json:"signature"`
	Doc       string `json:"doc"`
}

// ManifestFile is one entry in Manifest.Files.
type ManifestFile struct {
	IndexedAt string           `json:"indexedAt"`
	Language  string           `json:"language"`
	LineCount int              `json:"lineCount"`
	Symbols   []ManifestSymbol `json:"symbols"`
	Imports   []string         `json:"imports"`
	Exports   []string         `json:"exports"`
}

// Manifest is the full per-workspace manifest.
type Manifest struct {
	WorkspaceID string                  `json:"workspaceId"`
	IndexedAt   string                  `json:"indexedAt"`
	Files       map[string]ManifestFile `json:"files"`
}

// ManifestCache caches per-workspace manifests in memory, loading from disk
// on first miss. Thread-safe via a single RWMutex.
type ManifestCache struct {
	pathFor func(workspaceID string) string
	mu      sync.RWMutex
	cache   map[string]*Manifest
}

// NewManifestCache constructs a cache with the given workspace→path resolver.
// In production this is `config.ManifestCachePath`. Tests inject a fixed path.
func NewManifestCache(pathFor func(string) string) *ManifestCache {
	return &ManifestCache{pathFor: pathFor, cache: make(map[string]*Manifest)}
}

// Get returns the manifest for a workspace. Cache hit is O(1); miss reads
// the file once.
func (c *ManifestCache) Get(workspaceID string) (*Manifest, error) {
	c.mu.RLock()
	if m, ok := c.cache[workspaceID]; ok {
		c.mu.RUnlock()
		return m, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.cache[workspaceID]; ok {
		return m, nil
	}
	body, err := os.ReadFile(c.pathFor(workspaceID))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	c.cache[workspaceID] = &m
	return &m, nil
}

// FileForPath returns the per-file manifest entry, if present.
func (c *ManifestCache) FileForPath(workspaceID, path string) (ManifestFile, bool) {
	m, err := c.Get(workspaceID)
	if err != nil {
		return ManifestFile{}, false
	}
	mf, ok := m.Files[path]
	return mf, ok
}

// Invalidate drops the cached entry for a workspace (e.g., after `browzer sync`).
func (c *ManifestCache) Invalidate(workspaceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, workspaceID)
}
