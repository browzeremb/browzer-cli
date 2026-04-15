package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestCache_LoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	body := `{
	  "workspaceId": "ws_1",
	  "indexedAt": "2026-04-15T10:00:00Z",
	  "files": {
	    "src/foo.ts": {
	      "indexedAt": "2026-04-15T10:00:00Z",
	      "language": "typescript",
	      "lineCount": 80,
	      "symbols": [{"name":"foo","kind":"function","startLine":10,"endLine":25,"signature":"export function foo()","doc":""}],
	      "imports": ["./bar.ts"],
	      "exports": ["foo"]
	    }
	  }
	}`
	if err := os.WriteFile(manifestPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewManifestCache(func(string) string { return manifestPath })
	m, err := c.Get("ws_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := m.Files["src/foo.ts"].Symbols[0].Name; got != "foo" {
		t.Fatalf("Symbols[0].Name = %q, want foo", got)
	}
}

func TestManifestCache_Miss(t *testing.T) {
	c := NewManifestCache(func(string) string { return "/nonexistent/manifest.json" })
	if _, err := c.Get("ws_1"); err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

func TestManifestCache_FileForPath(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	_ = os.WriteFile(manifestPath, []byte(`{"workspaceId":"ws_1","indexedAt":"2026-04-15T10:00:00Z","files":{"src/foo.ts":{"indexedAt":"2026-04-15T10:00:00Z","language":"typescript","lineCount":1,"symbols":[],"imports":[],"exports":[]}}}`), 0o600)
	c := NewManifestCache(func(string) string { return manifestPath })
	if _, err := c.Get("ws_1"); err != nil {
		t.Fatal(err)
	}
	mf, ok := c.FileForPath("ws_1", "src/foo.ts")
	if !ok {
		t.Fatal("FileForPath should hit")
	}
	if mf.Language != "typescript" {
		t.Fatalf("language = %q", mf.Language)
	}
}
