package commands

import (
	"context"
	"testing"
)

// TestWorkspaceIndex_DelegatesToSyncFlow asserts that `workspace index` RunE
// calls runSyncFlowHook with SkipDocs=true and JSONMode="index" (Case I1).
// Uses the package-level runSyncFlowHook seam introduced in TASK_02.
func TestWorkspaceIndex_DelegatesToSyncFlow(t *testing.T) {
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
	root.SetArgs([]string{"workspace", "index"})
	_ = root.Execute()

	if !called {
		t.Fatal("runSyncFlowHook was not called — workspace index delegation not wired")
	}
	if !captured.SkipDocs {
		t.Errorf("SkipDocs = %v, want true", captured.SkipDocs)
	}
	if captured.SkipCode {
		t.Errorf("SkipCode = %v, want false", captured.SkipCode)
	}
	if captured.JSONMode != "index" {
		t.Errorf("JSONMode = %q, want \"index\"", captured.JSONMode)
	}
}

// TestWorkspaceIndex_FlagsRegistered asserts that --dry-run, --force, --json,
// --save are all present on `workspace index` (backward-compat). Case I2.
func TestWorkspaceIndex_FlagsRegistered(t *testing.T) {
	root := NewRootCommand("test")
	cmd, _, err := root.Find([]string{"workspace", "index"})
	if err != nil {
		t.Fatalf("find workspace index: %v", err)
	}
	for _, name := range []string{"dry-run", "force", "json", "save"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("flag --%s not registered on workspace index", name)
		}
	}
}
