package hooks

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// setupWorkspace prepares an isolated $HOME with a credentials file and a
// scratch workspace at <home>/work containing .browzer/config.json.
// Returns the workspace root absolute path.
func setupWorkspace(t *testing.T, withCreds bool, withConfig bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	if withCreds {
		if err := os.MkdirAll(filepath.Join(home, ".browzer"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, ".browzer", "credentials"), []byte("token"), 0o600); err != nil {
			t.Fatalf("write creds: %v", err)
		}
	}

	root := filepath.Join(home, "work")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if withConfig {
		if err := os.MkdirAll(filepath.Join(root, ".browzer"), 0o700); err != nil {
			t.Fatalf("mkdir .browzer: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".browzer", "config.json"), []byte("{}"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	return root
}

func TestIsInBrowzerWorkspace_BothPresent(t *testing.T) {
	root := setupWorkspace(t, true, true)
	if !IsInBrowzerWorkspace(root) {
		t.Fatalf("expected true when both creds and config are present")
	}
}

func TestIsInBrowzerWorkspace_NoCreds(t *testing.T) {
	root := setupWorkspace(t, false, true)
	if IsInBrowzerWorkspace(root) {
		t.Fatalf("expected false when credentials file missing")
	}
}

func TestIsInBrowzerWorkspace_NoConfig(t *testing.T) {
	root := setupWorkspace(t, true, false)
	if IsInBrowzerWorkspace(root) {
		t.Fatalf("expected false when .browzer/config.json missing")
	}
}

func TestIsInBrowzerWorkspace_WalkUpFromSubdirectory(t *testing.T) {
	root := setupWorkspace(t, true, true)
	deep := filepath.Join(root, "apps", "api", "src", "routes")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	if !IsInBrowzerWorkspace(deep) {
		t.Fatalf("expected true when cwd is several levels below workspace root")
	}
}

func TestIsInBrowzerWorkspace_WalkCapAt20Levels(t *testing.T) {
	root := setupWorkspace(t, true, true)
	// Build a directory chain 25 levels below root to exceed the cap.
	deep := root
	for range 25 {
		deep = filepath.Join(deep, "lvl")
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatalf("mkdir very deep: %v", err)
	}
	if IsInBrowzerWorkspace(deep) {
		t.Fatalf("expected false: 25 levels exceeds the 20-level cap")
	}
}

func TestIsInBrowzerWorkspace_EmptyCwdUsesGetwd(t *testing.T) {
	root := setupWorkspace(t, true, true)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if !IsInBrowzerWorkspace("") {
		t.Fatalf("empty cwd should fall back to os.Getwd()")
	}
}

func TestWorkspaceRootFor_Found(t *testing.T) {
	root := setupWorkspace(t, true, true)
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got := WorkspaceRootFor(deep)
	if got != root {
		t.Fatalf("WorkspaceRootFor(%q) = %q; want %q", deep, got, root)
	}
}

func TestWorkspaceRootFor_NotFound(t *testing.T) {
	// No .browzer/config.json anywhere — must return "".
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := WorkspaceRootFor(home); got != "" {
		t.Fatalf("expected empty string when no workspace, got %q", got)
	}
}

func TestWorkspaceRootFor_DoesNotRequireCreds(t *testing.T) {
	// Even without credentials, the workspace root resolver should
	// answer purely on the directory marker.
	root := setupWorkspace(t, false, true)
	if got := WorkspaceRootFor(root); got != root {
		t.Fatalf("WorkspaceRootFor should not depend on credentials: got %q; want %q", got, root)
	}
}

// clearCaches resets both per-process sync.Map caches so test cases that
// set different $HOME or $CLAUDE_PROJECT_DIR values don't bleed into each
// other. It iterates and deletes every stored key — sync.Map has no
// native Clear method before Go 1.23; this loop is portable to 1.21+.
func clearCaches() {
	workspaceCache.Range(func(k, _ any) bool { workspaceCache.Delete(k); return true })
	workspaceRootCache.Range(func(k, _ any) bool { workspaceRootCache.Delete(k); return true })
}

func TestIsInBrowzerWorkspace_CacheHitReturnsCorrectResult(t *testing.T) {
	clearCaches()
	t.Cleanup(clearCaches)

	root := setupWorkspace(t, true, true)

	// First call — populates cache.
	if !IsInBrowzerWorkspace(root) {
		t.Fatalf("first call expected true")
	}
	// Second call — must hit the cache (same key) and return the same result.
	if !IsInBrowzerWorkspace(root) {
		t.Fatalf("second call (cache hit) expected true")
	}
}

func TestWorkspaceRootFor_CacheHitReturnsCorrectResult(t *testing.T) {
	clearCaches()
	t.Cleanup(clearCaches)

	root := setupWorkspace(t, false, true)
	deep := filepath.Join(root, "src", "foo")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// First call — populates cache.
	got1 := WorkspaceRootFor(deep)
	if got1 != root {
		t.Fatalf("first call: got %q; want %q", got1, root)
	}
	// Second call — must hit the cache.
	got2 := WorkspaceRootFor(deep)
	if got2 != root {
		t.Fatalf("second call (cache hit): got %q; want %q", got2, root)
	}
}

func TestWorkspaceRootFor_CacheHitPerformsZeroStatCalls(t *testing.T) {
	clearCaches()
	t.Cleanup(clearCaches)

	root := setupWorkspace(t, false, true)

	// Warm the cache.
	if got := WorkspaceRootFor(root); got != root {
		t.Fatalf("warm: got %q; want %q", got, root)
	}

	// After warming, the cache entry exists. Delete the directory marker so
	// that any live filesystem walk would return "". If the cache works, the
	// result must still be the original root.
	if err := os.Remove(filepath.Join(root, ".browzer", "config.json")); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	got := WorkspaceRootFor(root)
	if got != root {
		t.Fatalf("cache miss detected (stat occurred after warm): got %q; want %q", got, root)
	}
}

func TestIsInBrowzerWorkspace_ClaudeProjectDirFastPath(t *testing.T) {
	clearCaches()
	t.Cleanup(clearCaches)

	root := setupWorkspace(t, true, true)
	// Point $CLAUDE_PROJECT_DIR at the workspace root — the fast-path
	// should resolve without walking up from cwd.
	t.Setenv("CLAUDE_PROJECT_DIR", root)

	// Use a cwd that is the root itself.
	if !IsInBrowzerWorkspace(root) {
		t.Fatalf("CLAUDE_PROJECT_DIR fast-path: expected true")
	}
}

func TestWorkspaceRootFor_ClaudeProjectDirFastPath(t *testing.T) {
	clearCaches()
	t.Cleanup(clearCaches)

	root := setupWorkspace(t, false, true)
	t.Setenv("CLAUDE_PROJECT_DIR", root)

	// Arbitrary cwd — $CLAUDE_PROJECT_DIR should take precedence over the walk.
	got := WorkspaceRootFor(root)
	if got != root {
		t.Fatalf("CLAUDE_PROJECT_DIR fast-path: got %q; want %q", got, root)
	}
}

func TestIsInBrowzerWorkspace_ConcurrentSameKey(t *testing.T) {
	clearCaches()
	t.Cleanup(clearCaches)

	root := setupWorkspace(t, true, true)

	const goroutines = 50
	results := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx] = IsInBrowzerWorkspace(root)
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if !r {
			t.Errorf("goroutine %d got false; want true", i)
		}
	}
}
