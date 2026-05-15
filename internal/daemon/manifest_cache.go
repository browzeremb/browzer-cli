package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
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

// FetchFn fetches a fresh manifest from the API. Used by the cache to
// recover from ENOENT on disk: a single-flight goroutine runs the
// fetcher, atomically persists the JSON to disk, and warms the in-memory
// cache so the NEXT Get for the same workspace hits without I/O.
//
// Token Economy v2.0.0 (FR-4): a missing manifest must not be a
// permanent regression to "minimal" — the daemon kicks a recovery
// fetch the first time it sees ENOENT.
type FetchFn func(ctx context.Context, workspaceID string) (*Manifest, error)

// fetchTimeout caps a single recovery fetch. The cache returns the
// original "not found" error to the caller immediately; the fetcher
// runs in the background and warms the cache for subsequent reads.
const fetchTimeout = 5 * time.Second

// ManifestCache caches per-workspace manifests in memory, loading from disk
// on first miss. Thread-safe via a single RWMutex. Optionally wired with a
// FetchFn for ENOENT recovery (single-flight via singleflight.Group).
type ManifestCache struct {
	pathFor      func(workspaceID string) string
	fetch        FetchFn // nil when constructed via NewManifestCache
	fetchTimeout time.Duration
	sf           singleflight.Group
	mu           sync.RWMutex
	cache        map[string]*Manifest
	// fetchDone, when non-nil, is signalled (closed-once via sync.Once) after
	// the singleflight fetcher goroutine returns. Tests inject this so they
	// can deterministically wait for the timeout-triggered fetcher to exit
	// without sleeping the full default 5s timeout. Production code never
	// sets this — kickRefetch checks the nil sentinel.
	fetchDone chan struct{}
}

// ManifestCacheOptions configures the optional knobs of NewManifestCacheWithFetchOptions.
//
// FetchTimeout, when > 0, overrides the package-level fetchTimeout (5s)
// applied to a single recovery fetch. Tests use this to drive the
// context.DeadlineExceeded path without waiting 5s of real time. A zero
// value falls back to the package default.
type ManifestCacheOptions struct {
	FetchTimeout time.Duration
	// fetchDone is test-only — see ManifestCache.fetchDone.
	fetchDone chan struct{}
}

// NewManifestCache constructs a cache with the given workspace→path resolver.
// In production this is `config.ManifestCachePath`. Tests inject a fixed path.
//
// Use NewManifestCacheWithFetch when ENOENT-recovery is needed.
func NewManifestCache(pathFor func(string) string) *ManifestCache {
	return &ManifestCache{pathFor: pathFor, cache: make(map[string]*Manifest)}
}

// NewManifestCacheWithFetch constructs a cache that recovers from missing
// manifest files by kicking a single-flight FetchFn in the background.
// FR-4 (Token Economy v2.0.0).
func NewManifestCacheWithFetch(pathFor func(string) string, fetch FetchFn) *ManifestCache {
	return NewManifestCacheWithFetchOptions(pathFor, fetch, ManifestCacheOptions{})
}

// NewManifestCacheWithFetchOptions is the configurable variant of
// NewManifestCacheWithFetch. Tests use it to shrink the per-fetch timeout
// down from the production default (5s) to a few hundred milliseconds so
// the timeout-cancels-fetcher invariant is exercised in CI-friendly time.
func NewManifestCacheWithFetchOptions(pathFor func(string) string, fetch FetchFn, opts ManifestCacheOptions) *ManifestCache {
	to := opts.FetchTimeout
	if to <= 0 {
		to = fetchTimeout
	}
	return &ManifestCache{
		pathFor:      pathFor,
		fetch:        fetch,
		fetchTimeout: to,
		cache:        make(map[string]*Manifest),
		fetchDone:    opts.fetchDone,
	}
}

// Get returns the manifest for a workspace. Cache hit is O(1); miss reads
// the file once OUTSIDE any held lock so concurrent Gets don't serialize on
// disk I/O. On ENOENT with a configured fetcher, kicks a background
// single-flight fetch (deduped per workspaceID) and returns the original
// not-found error so the current Read call falls back to "minimal" — the
// next Read after the fetcher completes will hit the warmed cache.
//
// Cache install uses a CAS check: if another goroutine raced ahead and
// installed an entry for the same workspaceID, we return that one instead
// so callers see a single shared *Manifest per workspace.
func (c *ManifestCache) Get(workspaceID string) (*Manifest, error) {
	c.mu.RLock()
	if m, ok := c.cache[workspaceID]; ok {
		c.mu.RUnlock()
		return m, nil
	}
	c.mu.RUnlock()

	// Disk read with no lock held.
	body, err := os.ReadFile(c.pathFor(workspaceID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && c.fetch != nil {
			c.kickRefetch(workspaceID)
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	c.mu.Lock()
	if existing, ok := c.cache[workspaceID]; ok {
		c.mu.Unlock()
		return existing, nil
	}
	c.cache[workspaceID] = &m
	c.mu.Unlock()
	return &m, nil
}

// kickRefetch starts a background recovery fetch for `workspaceID`,
// deduplicated by singleflight.Group. Any concurrent ENOENT for the same
// workspaceID coalesces into a single fetcher call. The fetcher's result
// is persisted via atomic tmp+rename and warmed into the in-memory cache;
// failures are dropped (best-effort) — the next ENOENT will retry.
func (c *ManifestCache) kickRefetch(workspaceID string) {
	go func() {
		if c.fetchDone != nil {
			defer func() {
				select {
				case <-c.fetchDone:
					// already closed by an earlier fetch
				default:
					close(c.fetchDone)
				}
			}()
		}
		to := c.fetchTimeout
		if to <= 0 {
			to = fetchTimeout
		}
		_, _, _ = c.sf.Do(workspaceID, func() (any, error) {
			ctx, cancel := context.WithTimeout(context.Background(), to)
			defer cancel()
			m, err := c.fetch(ctx, workspaceID)
			if err != nil || m == nil {
				return nil, err
			}
			body, err := json.MarshalIndent(m, "", "  ")
			if err != nil {
				return nil, err
			}
			body = append(body, '\n')
			path := c.pathFor(workspaceID)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, body, 0o644); err != nil {
				return nil, err
			}
			// F-004: compare-and-swap on Manifest.IndexedAt inside the
			// rename critical section. A slow daemon refetch must NOT
			// land after a fresher foreground sync. Re-read the on-disk
			// manifest's IndexedAt; if it's newer than what we just
			// fetched, abandon the write.
			c.mu.Lock()
			defer c.mu.Unlock()
			if existing, rerr := os.ReadFile(path); rerr == nil {
				var disk Manifest
				if json.Unmarshal(existing, &disk) == nil && disk.IndexedAt != "" && m.IndexedAt != "" {
					diskTS, derr := time.Parse(time.RFC3339, disk.IndexedAt)
					fetchedTS, ferr := time.Parse(time.RFC3339, m.IndexedAt)
					if derr == nil && ferr == nil && diskTS.After(fetchedTS) {
						_ = os.Remove(tmp)
						fmt.Fprintf(os.Stderr, "manifest_cache: skipping backwards manifest write for %s (disk=%s > fetched=%s)\n",
							workspaceID, disk.IndexedAt, m.IndexedAt)
						return nil, nil
					}
				}
			}
			if err := os.Rename(tmp, path); err != nil {
				_ = os.Remove(tmp)
				return nil, err
			}
			c.cache[workspaceID] = m
			return m, nil
		})
	}()
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
