package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/cache"
	"github.com/browzeremb/browzer-cli/internal/walker"
)

// TestMergeDocItems_UnionFlagsAndOrder pins mergeDocItems behavior:
// local-only, server-only, and shared entries are keyed by path,
// Indexed flips on for any server presence, Selected defaults to the
// indexed state, and output is sorted for deterministic rendering.
func TestMergeDocItems_UnionFlagsAndOrder(t *testing.T) {
	local := []walker.DocFile{
		{RelativePath: "b.md", AbsolutePath: "/repo/b.md", SHA256: "h-b", Size: 10},
		{RelativePath: "c.md", AbsolutePath: "/repo/c.md", SHA256: "h-c-local", Size: 20},
	}
	server := []api.IndexedDocument{
		{DocumentID: "d-a", RelativePath: "a.md", SizeBytes: 5, ChunkCount: 1, Status: "ready"},
		{DocumentID: "d-c", RelativePath: "c.md", SizeBytes: 20, ChunkCount: 2, Status: "ready"},
	}

	items := mergeDocItems(local, server)

	if len(items) != 3 {
		t.Fatalf("expected 3 merged items, got %d", len(items))
	}
	// Alphabetical order.
	wantPaths := []string{"a.md", "b.md", "c.md"}
	for i, p := range wantPaths {
		if items[i].RelativePath != p {
			t.Errorf("items[%d].RelativePath = %q, want %q", i, items[i].RelativePath, p)
		}
	}

	// a.md — server-only.
	a := items[0]
	if a.HasLocal() {
		t.Errorf("a.md should not have local")
	}
	if !a.Indexed || !a.Selected {
		t.Errorf("a.md should be indexed+selected by default")
	}
	if a.ServerDocumentID != "d-a" {
		t.Errorf("a.md ServerDocumentID = %q", a.ServerDocumentID)
	}

	// b.md — local-only (new).
	b := items[1]
	if !b.HasLocal() {
		t.Errorf("b.md should have local")
	}
	if b.Indexed {
		t.Errorf("b.md should not be indexed")
	}
	if b.Selected {
		t.Errorf("b.md should default to unselected (new item)")
	}

	// c.md — both sides.
	c := items[2]
	if !c.HasLocal() || !c.Indexed || !c.Selected {
		t.Errorf("c.md should have local+indexed+selected: %+v", c)
	}
	if c.LocalHash != "h-c-local" || c.ServerDocumentID != "d-c" {
		t.Errorf("c.md merge lost fields: %+v", c)
	}
}

// TestComputeDocDelta_Partitions verifies that computeDocDelta slots
// every selection combination into exactly one of the four buckets.
func TestComputeDocDelta_Partitions(t *testing.T) {
	docsCache := cache.DocsCache{
		Version: cache.CacheVersion,
		Files: map[string]cache.CachedDoc{
			"keep.md":     {SHA256: "h-keep"},
			"modified.md": {SHA256: "h-old"},
		},
	}

	items := []DocPickerItem{
		// Insert: local+selected, not indexed.
		{RelativePath: "new.md", LocalAbs: "/r/new.md", LocalHash: "h-new", LocalSize: 1, Selected: true},
		// Keep: local+indexed+selected, hash matches cache.
		{RelativePath: "keep.md", LocalAbs: "/r/keep.md", LocalHash: "h-keep", Indexed: true, Selected: true, ServerDocumentID: "d-keep"},
		// ReUpload: local+indexed+selected, hash differs.
		{RelativePath: "modified.md", LocalAbs: "/r/modified.md", LocalHash: "h-new-mod", Indexed: true, Selected: true, ServerDocumentID: "d-mod"},
		// Delete: indexed, unselected.
		{RelativePath: "gone.md", Indexed: true, Selected: false, ServerDocumentID: "d-gone"},
		// Ghost deselected — no effect.
		{RelativePath: "noise.md", Selected: false},
	}

	plan := computeDocDelta(items, docsCache)

	if len(plan.ToInsert) != 1 || plan.ToInsert[0].RelativePath != "new.md" {
		t.Errorf("ToInsert = %+v", plan.ToInsert)
	}
	if len(plan.ToKeep) != 1 || plan.ToKeep[0].RelativePath != "keep.md" {
		t.Errorf("ToKeep = %+v", plan.ToKeep)
	}
	if len(plan.ToReUpload) != 1 || plan.ToReUpload[0].RelativePath != "modified.md" {
		t.Errorf("ToReUpload = %+v", plan.ToReUpload)
	}
	if len(plan.ToDelete) != 1 || plan.ToDelete[0].RelativePath != "gone.md" {
		t.Errorf("ToDelete = %+v", plan.ToDelete)
	}
}

// TestRegisterWorkspaceDocs_HelpCompiles is a smoke test confirming the
// new commands register without panics and their --help strings are
// non-empty. Complements TestDualRegistration_* in root_test.go.
func TestRegisterWorkspaceDocs_HelpCompiles(t *testing.T) {
	root := NewRootCommand("test")
	for _, path := range [][]string{
		{"workspace", "index"},
		{"workspace", "docs"},
		{"index"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Errorf("find %v: %v", path, err)
			continue
		}
		if cmd.Short == "" {
			t.Errorf("%v: empty Short", path)
		}
	}
}

// ---------------------------------------------------------------------------
// TASK_02 new cases
// ---------------------------------------------------------------------------

// TestWorkspaceDocs_InteractiveFlagRegistered verifies the new --interactive
// flag is registered on `workspace docs` (Case M1).
func TestWorkspaceDocs_InteractiveFlagRegistered(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"workspace", "docs"})
	if err != nil {
		t.Fatalf("find workspace docs: %v", err)
	}
	f := cmd.Flags().Lookup("interactive")
	if f == nil {
		t.Fatal("--interactive flag not registered on workspace docs")
	}
	if f.DefValue != "false" {
		t.Errorf("--interactive default = %q, want \"false\"", f.DefValue)
	}
}

// TestWorkspaceDocs_NoFlagsDelegatesToSync verifies that the no-flags RunE
// delegates to runSyncFlowHook with SkipCode=true and JSONMode="sync"
// (Case M2). Uses the package-level runSyncFlowHook seam.
func TestWorkspaceDocs_NoFlagsDelegatesToSync(t *testing.T) {
	// Install a spy that captures opts and returns nil.
	var captured syncFlowOptions
	called := false
	orig := runSyncFlowHook
	t.Cleanup(func() { runSyncFlowHook = orig })
	runSyncFlowHook = func(_ context.Context, opts syncFlowOptions) error {
		captured = opts
		called = true
		return nil
	}

	root := NewRootCommand("test")
	root.SetArgs([]string{"workspace", "docs"})
	// Execute will fail at requireGitRoot unless we are inside a git repo,
	// but the spy intercepts before that point. The spy is called from
	// inside RunE, so we execute and then check the spy.
	_ = root.Execute()

	if !called {
		t.Fatal("runSyncFlowHook was not called — delegation not wired")
	}
	if !captured.SkipCode {
		t.Errorf("SkipCode = %v, want true", captured.SkipCode)
	}
	if captured.SkipDocs {
		t.Errorf("SkipDocs = %v, want false", captured.SkipDocs)
	}
	if captured.JSONMode != "sync" {
		t.Errorf("JSONMode = %q, want \"sync\"", captured.JSONMode)
	}
}

// TestWorkspaceDocs_AddPathUnchanged verifies that --add prevents the
// sync-delegation branch from firing (Case M3).
// The spy must NOT be called when --add is present — the command falls
// through to the spec-resolution path which needs network access, so it
// will error on requireGitRoot, but that error comes after the delegation
// guard passes. We just assert the spy was NOT invoked.
func TestWorkspaceDocs_AddPathUnchanged(t *testing.T) {
	orig := runSyncFlowHook
	t.Cleanup(func() { runSyncFlowHook = orig })
	syncCalled := false
	runSyncFlowHook = func(_ context.Context, _ syncFlowOptions) error {
		syncCalled = true
		return nil
	}

	root := NewRootCommand("test")
	root.SetArgs([]string{"workspace", "docs", "--add", "foo.md", "--yes"})
	_ = root.Execute()

	if syncCalled {
		t.Error("runSyncFlowHook must NOT be called when --add flag is set")
	}
}

// TestWorkspaceDocs_HelpIncludesInteractive verifies the Long docstring
// documents --interactive so users discover it via --help (Case M4).
func TestWorkspaceDocs_HelpIncludesInteractive(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"workspace", "docs"})
	if err != nil {
		t.Fatalf("find workspace docs: %v", err)
	}
	if !strings.Contains(cmd.Long, "--interactive") {
		t.Error("Long description does not mention --interactive")
	}
}
