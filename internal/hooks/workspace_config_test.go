package hooks

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// setupConfigWorkspace prepares an isolated workspace with a .browzer directory.
// When cfgJSON is non-empty it writes .browzer/config.json with that content;
// otherwise the config file is left absent to exercise the missing-file path.
func setupConfigWorkspace(t *testing.T, cfgJSON string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".browzer"), 0o700); err != nil {
		t.Fatalf("mkdir .browzer: %v", err)
	}
	if cfgJSON != "" {
		if err := os.WriteFile(filepath.Join(root, ".browzer", "config.json"), []byte(cfgJSON), 0o600); err != nil {
			t.Fatalf("write config.json: %v", err)
		}
	}
	return root
}

func TestWorkspaceGlobMode_BlockMode(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	root := setupConfigWorkspace(t, `{"hooks":{"glob":{"mode":"block"}}}`)
	if got := WorkspaceGlobMode(root); got != "block" {
		t.Errorf("WorkspaceGlobMode = %q; want %q", got, "block")
	}
}

func TestWorkspaceGlobMode_SoftMode(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	root := setupConfigWorkspace(t, `{"hooks":{"glob":{"mode":"soft"}}}`)
	if got := WorkspaceGlobMode(root); got != "soft" {
		t.Errorf("WorkspaceGlobMode = %q; want %q", got, "soft")
	}
}

func TestWorkspaceGlobMode_DefaultWhenModeAbsent(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	// Valid JSON but no hooks.glob.mode key → default "soft".
	root := setupConfigWorkspace(t, `{}`)
	if got := WorkspaceGlobMode(root); got != "soft" {
		t.Errorf("WorkspaceGlobMode = %q; want %q", got, "soft")
	}
}

func TestWorkspaceGlobMode_MissingConfig_ReturnsSoft(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	// No config.json written — missing file must return "soft".
	root := setupConfigWorkspace(t, "")
	if got := WorkspaceGlobMode(root); got != "soft" {
		t.Errorf("WorkspaceGlobMode = %q; want %q (missing config should default to soft)", got, "soft")
	}
}

func TestWorkspaceGlobMode_InvalidJSON_ReturnsSoft(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	root := setupConfigWorkspace(t, `{not valid json}`)
	if got := WorkspaceGlobMode(root); got != "soft" {
		t.Errorf("WorkspaceGlobMode = %q; want %q (bad JSON should default to soft)", got, "soft")
	}
}

func TestWorkspaceGlobMode_EmptyRoot_ReturnsSoft(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	if got := WorkspaceGlobMode(""); got != "soft" {
		t.Errorf("WorkspaceGlobMode(\"\") = %q; want %q", got, "soft")
	}
}

func TestWorkspaceGlobMode_CacheHitPerformsZeroIO(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	root := setupConfigWorkspace(t, `{"hooks":{"glob":{"mode":"block"}}}`)

	// Warm the cache.
	if got := WorkspaceGlobMode(root); got != "block" {
		t.Fatalf("warm: WorkspaceGlobMode = %q; want %q", got, "block")
	}

	// Delete the config file — if the cache works, the result must remain "block".
	if err := os.Remove(filepath.Join(root, ".browzer", "config.json")); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	if got := WorkspaceGlobMode(root); got != "block" {
		t.Errorf("cache miss detected after warm: WorkspaceGlobMode = %q; want %q", got, "block")
	}
}

// TestWorkspaceGlobMode_Concurrent verifies the sync.Map cache is safe under
// concurrent access. The -race flag will catch any data race.
//
// Note: t.Parallel() is intentionally absent — this test calls t.Setenv
// indirectly via setupConfigWorkspace (uses t.TempDir, not t.Setenv), but
// clearConfigCache modifies package-level state shared with other tests in
// this file. Keeping tests sequential prevents cache bleed without needing
// -race-only coverage.
func TestWorkspaceGlobMode_Concurrent(t *testing.T) {
	clearConfigCache()
	t.Cleanup(clearConfigCache)

	root := setupConfigWorkspace(t, `{"hooks":{"glob":{"mode":"block"}}}`)

	const goroutines = 50
	results := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx] = WorkspaceGlobMode(root)
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r != "block" {
			t.Errorf("goroutine %d: WorkspaceGlobMode = %q; want %q", i, r, "block")
		}
	}
}
