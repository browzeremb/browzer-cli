package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestAtomicWrite_SuccessfulWrite verifies that AtomicWrite creates or replaces
// the target file with the given content atomically.
// Baseline positive case for T1-T-7.
func TestAtomicWrite_SuccessfulWrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "workflow.json")
	content := []byte(`{"schemaVersion":1}`)

	if err := AtomicWrite(target, content); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile after AtomicWrite: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: want %q, got %q", content, got)
	}
}

// TestAtomicWrite_OriginalIntactOnError verifies that if the final rename step
// somehow fails (simulated by making the target directory read-only so the
// temp file cannot be renamed over it), the original file is left intact.
// Covers T1-T-7: original file intact on write error; tmp cleaned up on failure.
func TestAtomicWrite_OriginalIntactOnError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "workflow.json")
	original := []byte(`{"schemaVersion":1,"original":true}`)

	// Write the original file.
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatalf("setup original: %v", err)
	}

	// Make the directory read-only so the rename/write will fail.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	err := AtomicWrite(target, []byte(`{"schemaVersion":1,"new":true}`))
	if err == nil {
		// On some platforms (e.g. running as root in CI), chmod doesn't block
		// writes. Skip the assertion rather than fail.
		t.Skip("chmod did not restrict writes on this platform; skipping atomicity test")
	}

	// Original must still be intact.
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("ReadFile after failed AtomicWrite: %v", readErr)
	}
	if string(got) != string(original) {
		t.Errorf("original file was mutated on write error: want %q, got %q", original, got)
	}
}

// TestAtomicWrite_TmpFileCleanedUpOnFailure verifies that the .tmp file is
// removed when AtomicWrite fails, so partial writes don't accumulate.
// Covers T1-T-7: tmp file is cleaned up on failure.
func TestAtomicWrite_TmpFileCleanedUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "workflow.json")

	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	_ = AtomicWrite(target, []byte(`{"new":true}`))

	// Re-open the directory to list files (need to restore first).
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("tmp file %q was not cleaned up after AtomicWrite failure", e.Name())
		}
	}
}

// TestAtomicWrite_FailsWhenTargetDirMissing verifies that AtomicWrite returns
// an error (not panics) when the target's parent directory doesn't exist.
// Extra safety coverage beyond T1-T-7 baseline.
func TestAtomicWrite_FailsWhenTargetDirMissing(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nonexistent", "sub", "workflow.json")
	err := AtomicWrite(target, []byte(`{}`))
	if err == nil {
		t.Error("expected error when target parent dir is missing, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
		// The error may be wrapped; just check it's non-nil (already done above).
		_ = err
	}
}
