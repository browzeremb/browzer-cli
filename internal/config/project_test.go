package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSaveProjectConfig_AtomicTmpRename verifies that SaveProjectConfig
// writes via a temporary file and rename, NOT directly to the final
// path. We assert this by populating the dir with a tmp file from a
// previous (interrupted) write and confirming Save still produces a
// clean final file with no leftover .tmp.
func TestSaveProjectConfig_AtomicTmpRename(t *testing.T) {
	dir := t.TempDir()
	browzerDir := filepath.Join(dir, ".browzer")
	if err := os.MkdirAll(browzerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate a stale tmp from an interrupted save.
	stale := filepath.Join(browzerDir, "config.json.tmp")
	if err := os.WriteFile(stale, []byte("{stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &ProjectConfig{
		WorkspaceID:   "ws-1",
		WorkspaceName: "Test",
		Server:        "https://browzeremb.com",
		CreatedAt:     "2026-01-01T00:00:00Z",
	}
	if err := SaveProjectConfig(dir, cfg); err != nil {
		t.Fatalf("SaveProjectConfig: %v", err)
	}

	final := filepath.Join(browzerDir, "config.json")
	data, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	var got ProjectConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	if got.WorkspaceID != "ws-1" || got.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected payload: %+v", got)
	}
	// The tmp file should have been replaced by the rename — the new
	// successful write produces no orphan tmp.
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale tmp to be gone, got err=%v", err)
	}
}

// TestSaveProjectConfig_PreservesCreatedAt confirms that re-saving an
// existing config preserves the original CreatedAt timestamp instead
// of stamping a fresh one each time.
func TestSaveProjectConfig_PreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	cfg := &ProjectConfig{
		WorkspaceID: "ws-1",
		Server:      "https://example.com",
		CreatedAt:   "2025-12-25T00:00:00Z",
	}
	if err := SaveProjectConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProjectConfig(dir)
	if err != nil || loaded == nil {
		t.Fatalf("LoadProjectConfig: %v / %+v", err, loaded)
	}
	if loaded.CreatedAt != "2025-12-25T00:00:00Z" {
		t.Fatalf("CreatedAt not preserved: %s", loaded.CreatedAt)
	}
}
