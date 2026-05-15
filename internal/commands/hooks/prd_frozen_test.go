package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// TestPrdFrozenDecide exercises the prd-frozen guard's three cases.
func TestPrdFrozenDecide(t *testing.T) {
	t.Parallel()

	// makePRDInput builds a minimal PreToolUse JSON payload for the given
	// tool name and file path (using the "file_path" key).
	makePRDInput := func(t *testing.T, toolName, filePath string) []byte {
		t.Helper()
		payload := map[string]any{
			"tool_name": toolName,
			"tool_input": map[string]any{
				"file_path": filePath,
			},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal input: %v", err)
		}
		return data
	}

	// makeNotebookInput builds a payload that uses the "notebook_path" key,
	// as the NotebookEdit tool emits.
	makeNotebookInput := func(t *testing.T, notebookPath string) []byte {
		t.Helper()
		payload := map[string]any{
			"tool_name": "NotebookEdit",
			"tool_input": map[string]any{
				"notebook_path": notebookPath,
			},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal input: %v", err)
		}
		return data
	}

	// frozenStaging creates a temp staging dir, writes PRD.md and the
	// .prd-frozen sentinel, and returns the path to PRD.md.
	frozenStaging := func(t *testing.T) string {
		t.Helper()
		tmp := t.TempDir()
		stagingDir := filepath.Join(tmp, "staging")
		if err := os.MkdirAll(stagingDir, 0o700); err != nil {
			t.Fatalf("mkdir staging: %v", err)
		}
		prdPath := filepath.Join(stagingDir, "PRD.md")
		if err := os.WriteFile(prdPath, []byte("# PRD"), 0o600); err != nil {
			t.Fatalf("write PRD.md: %v", err)
		}
		sentinel := filepath.Join(stagingDir, ".prd-frozen")
		if err := os.WriteFile(sentinel, []byte{}, 0o600); err != nil {
			t.Fatalf("write .prd-frozen: %v", err)
		}
		return prdPath
	}

	t.Run("PRD.md without frozen marker passes through", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		stagingDir := filepath.Join(tmp, "staging")
		if err := os.MkdirAll(stagingDir, 0o700); err != nil {
			t.Fatalf("mkdir staging: %v", err)
		}
		prdPath := filepath.Join(stagingDir, "PRD.md")
		if err := os.WriteFile(prdPath, []byte("# PRD"), 0o600); err != nil {
			t.Fatalf("write PRD.md: %v", err)
		}

		raw := makePRDInput(t, "Edit", prdPath)
		got := prdFrozenDecide(raw)
		if got != internalhooks.ExitPassthrough {
			t.Errorf("expected ExitPassthrough (%d), got %d", internalhooks.ExitPassthrough, got)
		}
	})

	t.Run("PRD.md with .prd-frozen sentinel denies", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		stagingDir := filepath.Join(tmp, "staging")
		if err := os.MkdirAll(stagingDir, 0o700); err != nil {
			t.Fatalf("mkdir staging: %v", err)
		}
		prdPath := filepath.Join(stagingDir, "PRD.md")
		if err := os.WriteFile(prdPath, []byte("# PRD"), 0o600); err != nil {
			t.Fatalf("write PRD.md: %v", err)
		}
		sentinel := filepath.Join(stagingDir, ".prd-frozen")
		if err := os.WriteFile(sentinel, []byte{}, 0o600); err != nil {
			t.Fatalf("write .prd-frozen: %v", err)
		}

		raw := makePRDInput(t, "Write", prdPath)
		got := prdFrozenDecide(raw)
		if got != internalhooks.ExitDeny {
			t.Errorf("expected ExitDeny (%d), got %d", internalhooks.ExitDeny, got)
		}
	})

	t.Run("non-PRD path passes through", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		stagingDir := filepath.Join(tmp, "staging")
		if err := os.MkdirAll(stagingDir, 0o700); err != nil {
			t.Fatalf("mkdir staging: %v", err)
		}
		cfgPath := filepath.Join(stagingDir, "CONFIG.md")
		if err := os.WriteFile(cfgPath, []byte("# config"), 0o600); err != nil {
			t.Fatalf("write CONFIG.md: %v", err)
		}
		// Even with a .prd-frozen sentinel present, CONFIG.md must pass through.
		sentinel := filepath.Join(stagingDir, ".prd-frozen")
		if err := os.WriteFile(sentinel, []byte{}, 0o600); err != nil {
			t.Fatalf("write .prd-frozen: %v", err)
		}

		raw := makePRDInput(t, "Edit", cfgPath)
		got := prdFrozenDecide(raw)
		if got != internalhooks.ExitPassthrough {
			t.Errorf("expected ExitPassthrough (%d), got %d", internalhooks.ExitPassthrough, got)
		}
	})

	t.Run("MultiEdit on frozen PRD.md denies", func(t *testing.T) {
		t.Parallel()
		prdPath := frozenStaging(t)

		raw := makePRDInput(t, "MultiEdit", prdPath)
		got := prdFrozenDecide(raw)
		if got != internalhooks.ExitDeny {
			t.Errorf("expected ExitDeny (%d), got %d", internalhooks.ExitDeny, got)
		}
	})

	t.Run("NotebookEdit on frozen PRD.md denies via notebook_path", func(t *testing.T) {
		t.Parallel()
		prdPath := frozenStaging(t)

		raw := makeNotebookInput(t, prdPath)
		got := prdFrozenDecide(raw)
		if got != internalhooks.ExitDeny {
			t.Errorf("expected ExitDeny (%d), got %d", internalhooks.ExitDeny, got)
		}
	})

	t.Run("unknown tool passes through", func(t *testing.T) {
		t.Parallel()
		prdPath := frozenStaging(t)

		raw := makePRDInput(t, "SomeOtherTool", prdPath)
		got := prdFrozenDecide(raw)
		if got != internalhooks.ExitPassthrough {
			t.Errorf("expected ExitPassthrough (%d), got %d", internalhooks.ExitPassthrough, got)
		}
	})
}
