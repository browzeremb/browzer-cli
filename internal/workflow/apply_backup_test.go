package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotateWorkflowBackups_ProducesNumericChain (B6 / FR-3, 2026-05-07):
// after K mutations against the same workflow.json, the directory
// contains at most workflowBackupCount (now 2) numbered backups.
// Uses append-step (a kept mutator) to trigger backup rotation.
func TestRotateWorkflowBackups_ProducesNumericChain(t *testing.T) {
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")

	// Seed an initial document.
	if err := os.WriteFile(path, []byte(makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Apply 3 distinct mutations so rotation passes the workflowBackupCount
	// cap (2) and we can prove only .bak.1 and .bak.2 survive, not .bak.3.
	for i := 0; i < 3; i++ {
		stepID := fmt.Sprintf("STEP_%02d_EXTRA", i+10)
		payload, _ := json.Marshal(map[string]any{
			"stepId":        stepID,
			"name":          "TASK",
			"taskId":        fmt.Sprintf("TASK_%02d", i+10),
			"status":        "PENDING",
			"applicability": map[string]any{"applicable": true, "reason": "default"},
			"completedAt":   nil,
			"owner":         nil,
			"worktrees":     map[string]any{"used": false, "worktrees": []any{}},
			"task":          map[string]any{},
		})
		_, err := ApplyAndPersist(path, "append-step", MutatorArgs{
			Payload: payload,
		}, false)
		if err != nil {
			t.Fatalf("apply iteration %d: %v", i, err)
		}
	}

	// .bak.1 and .bak.2 must exist; .bak.3 must NOT (cap=2).
	for i := 1; i <= workflowBackupCount; i++ {
		bak := fmt.Sprintf("%s.bak.%d", path, i)
		if _, err := os.Stat(bak); err != nil {
			t.Errorf("expected %s to exist; got %v", bak, err)
		}
	}
	excess := fmt.Sprintf("%s.bak.%d", path, workflowBackupCount+1)
	if _, err := os.Stat(excess); err == nil {
		t.Errorf("rotation cap exceeded — %s should not exist", excess)
	}

	// .bak.1 must contain fewer steps than the current workflow (one
	// append-step behind); .bak.2 must have even fewer. Both must be
	// valid JSON with a "steps" array.
	bak1, err1 := os.ReadFile(path + ".bak.1")
	bak2, err2 := os.ReadFile(path + ".bak.2")
	current, err3 := os.ReadFile(path)
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("backup read errors: %v / %v / %v", err1, err2, err3)
	}
	stepCount := func(b []byte) int {
		var doc struct {
			Steps []any `json:"steps"`
		}
		_ = json.Unmarshal(b, &doc)
		return len(doc.Steps)
	}
	if stepCount(bak2) >= stepCount(bak1) {
		t.Errorf("expected bak.2 step count < bak.1: bak2=%d bak1=%d", stepCount(bak2), stepCount(bak1))
	}
	if stepCount(bak1) >= stepCount(current) {
		t.Errorf("expected bak.1 step count < current: bak1=%d current=%d", stepCount(bak1), stepCount(current))
	}
}

// TestRotateWorkflowBackups_FirstWriteSkipsBackup (B6, 2026-05-05):
// when workflow.json doesn't exist on disk, rotation is a no-op
// (no `.bak.*` materialised). The mutation itself still proceeds
// — current behaviour for the daemon's first append-step is not
// affected.
func TestRotateWorkflowBackups_FirstWriteSkipsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	rotateWorkflowBackups(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Errorf("unexpected backup file %s for non-existent source", e.Name())
		}
	}
}

