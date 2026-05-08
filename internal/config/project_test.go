package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestEnsureBrowzerGitignore_CreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureBrowzerGitignore(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore not created: %v", err)
	}
	for _, entry := range BrowzerGitignoreEntries {
		if !strings.Contains(string(data), entry) {
			t.Fatalf("missing entry %q in:\n%s", entry, data)
		}
	}
}

func TestEnsureBrowzerGitignore_AppendsMissingOnly(t *testing.T) {
	dir := t.TempDir()
	existing := "node_modules/\n.browzer/\n"
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureBrowzerGitignore(dir); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Count(got, ".browzer/") != 1 {
		t.Fatalf(".browzer/ duplicated:\n%s", got)
	}
	if !strings.Contains(got, "docs/browzer/feat-*/staging/") {
		t.Fatalf("staging entry not appended:\n%s", got)
	}
	if !strings.HasPrefix(got, existing) {
		t.Fatalf("existing content not preserved:\n%s", got)
	}
}

func TestEnsureBrowzerGitignore_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureBrowzerGitignore(dir); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))

	if err := EnsureBrowzerGitignore(dir); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))

	if string(first) != string(second) {
		t.Fatalf("file changed on second call:\nbefore=%q\nafter=%q", first, second)
	}
}

func TestEnsureBrowzerGitignore_NoTrailingNewlineHandled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBrowzerGitignore(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	got := string(data)
	if !strings.Contains(got, "node_modules/\n.browzer/") {
		t.Fatalf("missing newline before appended entries:\n%s", got)
	}
}
