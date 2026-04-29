package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── WorkspaceManifest (existing) tests ───────────────────────────────────────

// TestWorkspaceManifest_UpsertGetRemove exercises the in-memory CRUD helpers.
func TestWorkspaceManifest_UpsertGetRemove(t *testing.T) {
	m := &WorkspaceManifest{entries: make(map[string]ManifestEntry)}

	entry := ManifestEntry{
		ID:        "ws-1",
		Name:      "my-workspace",
		RootPath:  "/repo",
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	m.Upsert(entry)

	got, ok := m.Get("ws-1")
	if !ok {
		t.Fatal("Get: expected entry to exist after Upsert")
	}
	if got.Name != "my-workspace" {
		t.Errorf("Get: expected Name=my-workspace, got %q", got.Name)
	}

	m.Remove("ws-1")
	if _, ok := m.Get("ws-1"); ok {
		t.Error("Remove: expected entry to be gone")
	}
}

// TestWorkspaceManifest_SaveLoad exercises round-trip serialization.
func TestWorkspaceManifest_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	orgID := "org-123"

	m := &WorkspaceManifest{entries: make(map[string]ManifestEntry)}
	now := time.Now().UTC().Truncate(time.Second)
	m.Upsert(ManifestEntry{ID: "ws-a", Name: "Alpha", RootPath: "/alpha", UpdatedAt: now})
	m.Upsert(ManifestEntry{ID: "ws-b", Name: "Beta", RootPath: "/beta", UpdatedAt: now, LocallyModified: true})

	if err := m.Save(dir, orgID); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadWorkspaceManifest(dir, orgID)
	if err != nil {
		t.Fatalf("LoadWorkspaceManifest: %v", err)
	}
	if e, ok := loaded.Get("ws-a"); !ok || e.Name != "Alpha" {
		t.Errorf("round-trip: expected ws-a/Alpha, got %v %v", ok, e)
	}
	if e, ok := loaded.Get("ws-b"); !ok || !e.LocallyModified {
		t.Errorf("round-trip: expected ws-b.LocallyModified=true, got %v %v", ok, e)
	}
}

// TestLoadWorkspaceManifest_MissingFile asserts a non-existent manifest returns
// an empty (non-nil) manifest rather than an error.
func TestLoadWorkspaceManifest_MissingFile(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadWorkspaceManifest(dir, "org-missing")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil manifest for missing file")
	}
	if all := m.All(); len(all) != 0 {
		t.Errorf("expected 0 entries, got %d", len(all))
	}
}

// ── FeatureCache tests ────────────────────────────────────────────────────────

// TestFeatureCacheEntry_RoundTrip exercises JSON serialization of
// FeatureCacheEntry so schema drift (renamed fields, missing omitempty, etc.)
// breaks a test rather than silently corrupting the cache file.
func TestFeatureCacheEntry_RoundTrip(t *testing.T) {
	warmedAt := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	original := FeatureCacheEntry{
		FeatureID:     "feat-20260429-cache-layer",
		WarmedAt:      warmedAt,
		WarmedAtSHA:   "abc123def456",
		CacheDir:      "/repo/docs/browzer/feat-20260429-cache-layer/.browzer-cache",
		DepsFiles:     []string{"apps/api/src/routes/ask.ts", "apps/api/src/server.ts"},
		MentionsFiles: []string{"apps/api/src/routes/ask.ts"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded FeatureCacheEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.FeatureID != original.FeatureID {
		t.Errorf("FeatureID: want %q, got %q", original.FeatureID, decoded.FeatureID)
	}
	if !decoded.WarmedAt.Equal(original.WarmedAt) {
		t.Errorf("WarmedAt: want %v, got %v", original.WarmedAt, decoded.WarmedAt)
	}
	if decoded.WarmedAtSHA != original.WarmedAtSHA {
		t.Errorf("WarmedAtSHA: want %q, got %q", original.WarmedAtSHA, decoded.WarmedAtSHA)
	}
	if decoded.CacheDir != original.CacheDir {
		t.Errorf("CacheDir: want %q, got %q", original.CacheDir, decoded.CacheDir)
	}
	if len(decoded.DepsFiles) != len(original.DepsFiles) {
		t.Errorf("DepsFiles: want %v, got %v", original.DepsFiles, decoded.DepsFiles)
	}
	if len(decoded.MentionsFiles) != len(original.MentionsFiles) {
		t.Errorf("MentionsFiles: want %v, got %v", original.MentionsFiles, decoded.MentionsFiles)
	}
}

// TestSaveLoadFeatureCache exercises the SaveFeatureCache / LoadFeatureCache
// round-trip. Asserts all entries survive the tmp+rename atomic write.
func TestSaveLoadFeatureCache(t *testing.T) {
	dir := t.TempDir()
	warmedAt := time.Date(2026, 4, 29, 8, 0, 0, 0, time.UTC)

	cache := map[string]FeatureCacheEntry{
		"feat-alpha": {
			FeatureID:     "feat-alpha",
			WarmedAt:      warmedAt,
			WarmedAtSHA:   "sha-alpha",
			CacheDir:      "/repo/docs/browzer/feat-alpha/.browzer-cache",
			DepsFiles:     []string{"apps/api/src/server.ts"},
			MentionsFiles: []string{"apps/api/src/server.ts"},
		},
		"feat-beta": {
			FeatureID:     "feat-beta",
			WarmedAt:      warmedAt,
			WarmedAtSHA:   "sha-beta",
			CacheDir:      "/repo/docs/browzer/feat-beta/.browzer-cache",
			DepsFiles:     []string{},
			MentionsFiles: []string{},
		},
	}

	if err := SaveFeatureCache(dir, cache); err != nil {
		t.Fatalf("SaveFeatureCache: %v", err)
	}

	// Verify the file exists.
	expectedPath := filepath.Join(dir, "feature-cache.json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("expected file %q to exist after Save", expectedPath)
	}

	loaded, err := LoadFeatureCache(dir)
	if err != nil {
		t.Fatalf("LoadFeatureCache: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}
	alpha, ok := loaded["feat-alpha"]
	if !ok {
		t.Fatal("loaded cache missing feat-alpha")
	}
	if alpha.WarmedAtSHA != "sha-alpha" {
		t.Errorf("feat-alpha WarmedAtSHA: want sha-alpha, got %q", alpha.WarmedAtSHA)
	}
	if !alpha.WarmedAt.Equal(warmedAt) {
		t.Errorf("feat-alpha WarmedAt: want %v, got %v", warmedAt, alpha.WarmedAt)
	}
}

// TestLoadFeatureCache_Missing asserts that a missing file returns an empty
// (non-nil) map rather than an error.
func TestLoadFeatureCache_Missing(t *testing.T) {
	dir := t.TempDir()
	cache, err := LoadFeatureCache(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing feature-cache, got %v", err)
	}
	if cache == nil {
		t.Fatal("expected non-nil map for missing file")
	}
	if len(cache) != 0 {
		t.Errorf("expected 0 entries, got %d", len(cache))
	}
}

// TestLoadFeatureCache_CorruptFile asserts that a malformed JSON file returns
// an empty map (graceful degradation) rather than surfacing a parse error.
func TestLoadFeatureCache_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "feature-cache.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	cache, err := LoadFeatureCache(dir)
	if err != nil {
		t.Fatalf("expected nil error for corrupt file (graceful degradation), got %v", err)
	}
	if len(cache) != 0 {
		t.Errorf("expected 0 entries for corrupt file, got %d", len(cache))
	}
}

// TestIsCacheStale_SHAMismatch asserts that IsCacheStale returns true when the
// stored SHA differs from the current HEAD. This is the core Risk Checkpoint #7
// guard — a stale cache must not silently feed wrong blast-radius data.
func TestIsCacheStale_SHAMismatch(t *testing.T) {
	cache := map[string]FeatureCacheEntry{
		"feat-x": {
			FeatureID:   "feat-x",
			WarmedAtSHA: "old-sha-111",
		},
	}

	if !IsCacheStale(cache, "feat-x", "new-sha-222") {
		t.Error("expected IsCacheStale=true when SHA changed, got false")
	}
}

// TestIsCacheStale_SHAMatch asserts that IsCacheStale returns false when the
// stored SHA matches the current HEAD.
func TestIsCacheStale_SHAMatch(t *testing.T) {
	sha := "abc123def456"
	cache := map[string]FeatureCacheEntry{
		"feat-y": {
			FeatureID:   "feat-y",
			WarmedAtSHA: sha,
		},
	}

	if IsCacheStale(cache, "feat-y", sha) {
		t.Error("expected IsCacheStale=false when SHA matches, got true")
	}
}

// TestIsCacheStale_MissingEntry asserts that IsCacheStale returns true when
// no entry exists for the given featureId (fresh feature, never warmed).
func TestIsCacheStale_MissingEntry(t *testing.T) {
	cache := map[string]FeatureCacheEntry{}

	if !IsCacheStale(cache, "feat-not-warmed", "any-sha") {
		t.Error("expected IsCacheStale=true for missing entry, got false")
	}
}
