package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// writeWorkflowFile is a shared test helper. Originally defined in
// workflow_get_step_test.go before that file was deleted in Wave 1A; restored
// here so the remaining append-step / append-steps tests still compile.
func writeWorkflowFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writeWorkflowFile: %v", err)
	}
	return wfPath
}
