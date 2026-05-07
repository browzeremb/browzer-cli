package commands

// F-8 integration tests for `workflow init --mode`.
//
// AC-1: `browzer workflow init --mode review` seeds config.mode = "review".
// AC-2: `browzer workflow init --mode bogus` exits non-zero.
// AC-3: default mode (no --mode flag) remains "autonomous".

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflowInitPath builds the expected workflow.json path for feat under root.
func workflowInitPath(root, feat string) string {
	return filepath.Join(root, "docs", "browzer", feat, "workflow.json")
}

// TestWorkflowInit_ModeReview_SeedsConfigMode asserts that
// `workflow init --mode review` writes config.mode = "review" (F-8 AC-1).
func TestWorkflowInit_ModeReview_SeedsConfigMode(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	feat := "feat-20260507-init-review"
	wfPath := workflowInitPath(root, feat)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"workflow", "init",
		"--feature-id", feat,
		"--workflow", wfPath,
		"--mode", "review",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workflow init --mode review failed: %v", err)
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("read workflow.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal workflow.json: %v", err)
	}
	cfg, _ := doc["config"].(map[string]any)
	if cfg == nil {
		t.Fatal("config block missing from seeded workflow.json")
	}
	mode, _ := cfg["mode"].(string)
	if mode != "review" {
		t.Errorf("expected config.mode = %q; got %q", "review", mode)
	}
}

// TestWorkflowInit_ModeAutonomous_IsDefault asserts that when --mode is
// omitted, config.mode defaults to "autonomous" (F-8 AC-3).
func TestWorkflowInit_ModeAutonomous_IsDefault(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	feat := "feat-20260507-init-default"
	wfPath := workflowInitPath(root, feat)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"workflow", "init",
		"--feature-id", feat,
		"--workflow", wfPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workflow init (no --mode) failed: %v", err)
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("read workflow.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal workflow.json: %v", err)
	}
	cfg, _ := doc["config"].(map[string]any)
	if cfg == nil {
		t.Fatal("config block missing")
	}
	mode, _ := cfg["mode"].(string)
	if mode != "autonomous" {
		t.Errorf("expected default config.mode = %q; got %q", "autonomous", mode)
	}
}

// TestWorkflowInit_ModeBogus_RejectsWithNonZeroExit asserts that
// `workflow init --mode bogus` exits non-zero (F-8 AC-2).
func TestWorkflowInit_ModeBogus_RejectsWithNonZeroExit(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	feat := "feat-20260507-init-bogus"
	wfPath := workflowInitPath(root, feat)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{
		"workflow", "init",
		"--feature-id", feat,
		"--workflow", wfPath,
		"--mode", "bogus",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-zero exit for --mode bogus; got success")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bogus") && !strings.Contains(msg, "invalid") {
		t.Errorf("expected error to mention invalid mode; got: %s", msg)
	}

	// workflow.json must NOT have been created.
	if _, statErr := os.Stat(wfPath); statErr == nil {
		t.Errorf("workflow.json should not exist after rejected init; path=%s", wfPath)
	}
}
