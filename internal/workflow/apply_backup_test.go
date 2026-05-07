package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotateWorkflowBackups_ProducesNumericChain (B6 / FR-3, 2026-05-07):
// after K mutations against the same workflow.json, the directory
// contains at most workflowBackupCount (now 2) numbered backups, each
// holding the SAME contents as the workflow had K-i mutations ago.
func TestRotateWorkflowBackups_ProducesNumericChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")

	// Seed an initial document.
	if err := os.WriteFile(path, []byte(makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Apply 3 distinct mutations so rotation passes the workflowBackupCount
	// cap (2) and we can prove only .bak.1 and .bak.2 survive, not .bak.3.
	for i := 0; i < 3; i++ {
		_, err := ApplyAndPersist(path, "patch", MutatorArgs{
			JQExpr: fmt.Sprintf(`.featureName = "tag-%d"`, i),
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

	// .bak.1 must contain the document one mutation behind the current
	// state ("tag-1"), and .bak.2 must contain the document two
	// mutations behind ("tag-0"). The rotation walks from oldest →
	// newest before copying current → .bak.1, so:
	//   current  = tag-2
	//   .bak.1   = tag-1
	//   .bak.2   = tag-0
	expectations := map[string]string{
		path + ".bak.1": `"tag-1"`,
		path + ".bak.2": `"tag-0"`,
	}
	for fp, marker := range expectations {
		data, err := os.ReadFile(fp)
		if err != nil {
			t.Errorf("read %s: %v", fp, err)
			continue
		}
		if !strings.Contains(string(data), marker) {
			t.Errorf("%s expected to contain %s; got first 100 bytes: %q",
				fp, marker, truncateForTest(string(data), 100))
		}
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

func truncateForTest(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
