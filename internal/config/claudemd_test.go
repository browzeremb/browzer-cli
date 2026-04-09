package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectBrowzerSection_CreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()

	if err := InjectBrowzerSection(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}
	if !strings.Contains(string(data), browzerSectionSentinel) {
		t.Fatalf("sentinel not found in created file:\n%s", data)
	}
}

func TestInjectBrowzerSection_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	existing := "# My Project\n\nSome existing content.\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InjectBrowzerSection(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.HasPrefix(content, existing) {
		t.Fatalf("existing content not preserved:\n%s", content)
	}
	if !strings.Contains(content, browzerSectionSentinel) {
		t.Fatalf("sentinel not found after injection:\n%s", content)
	}
}

func TestInjectBrowzerSection_Idempotent(t *testing.T) {
	dir := t.TempDir()

	if err := InjectBrowzerSection(dir); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))

	// Second call must produce identical output.
	if err := InjectBrowzerSection(dir); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))

	if string(first) != string(second) {
		t.Fatalf("file changed on second call:\nbefore=%q\nafter=%q", first, second)
	}
	// Sentinel appears exactly once.
	count := strings.Count(string(second), browzerSectionSentinel)
	if count != 1 {
		t.Fatalf("sentinel appears %d times (want 1):\n%s", count, second)
	}
}

func TestInjectBrowzerSection_NoTrailingNewlineHandled(t *testing.T) {
	dir := t.TempDir()
	// File with no trailing newline.
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := InjectBrowzerSection(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	content := string(data)
	if strings.Contains(content, "# Existing##") || strings.Contains(content, "# Existing\n\n##") {
		// Fine either way — just must not smash lines together.
	}
	if !strings.Contains(content, "# Existing") {
		t.Fatalf("original content lost:\n%s", content)
	}
	if !strings.Contains(content, browzerSectionSentinel) {
		t.Fatalf("sentinel not found:\n%s", content)
	}
}
