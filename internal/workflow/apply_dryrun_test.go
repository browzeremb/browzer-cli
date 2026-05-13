package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newStepPayload returns a minimal valid TASK step JSON payload for dry-run tests.
// taskID must match ^TASK_[0-9]{2}$.
func newStepPayload(t *testing.T, stepID, taskID string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"stepId":        stepID,
		"name":          "TASK",
		"taskId":        taskID,
		"status":        "PENDING",
		"startedAt":     "2026-05-01T10:00:00Z",
		"applicability": map[string]any{"applicable": true, "reason": "default"},
		"completedAt":   nil,
		"owner":         nil,
		"worktrees":     map[string]any{"used": false, "worktrees": []any{}},
		"task":          map[string]any{},
	})
	if err != nil {
		t.Fatalf("newStepPayload marshal: %v", err)
	}
	return b
}

// TestApplyDryRun_DoesNotMutateFile (A1.4, 2026-05-05): the dry-run
// pipeline must validate AND emit a structured response WITHOUT
// touching workflow.json on disk. We snapshot mtime + content
// before and after to assert byte-stability. Uses append-step to
// trigger a real mutation (patch verb was retired in v3.0.0).
func TestApplyDryRun_DoesNotMutateFile(t *testing.T) {
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")
	original := makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	res, err := ApplyDryRun(path, "append-step", MutatorArgs{
		Payload: newStepPayload(t, "STEP_10_TASK_10", "TASK_10"),
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !res.Ok {
		t.Errorf("expected ok=true, got errors=%v", res.Errors)
	}
	if res.BeforeHash == res.AfterHash {
		t.Errorf("expected before/after hashes to differ on a real mutation")
	}
	if !contains(res.DiffPaths, "steps.length") && !contains(res.DiffPaths, "steps") {
		t.Errorf("expected diffPaths to include steps change; got %v", res.DiffPaths)
	}

	// File MUST be byte-identical and mtime-stable.
	post, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(post) != original {
		t.Errorf("workflow.json was modified by dry-run\nbefore: %s\nafter:  %s",
			truncate(original, 200), truncate(string(post), 200))
	}
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("re-stat: %v", err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("mtime changed under dry-run: %v → %v", st1.ModTime(), st2.ModTime())
	}
}

// TestApplyDryRun_ReportsValidationErrors (A1.4, 2026-05-05): when
// the post-mutation document fails CUE validation, ApplyDryRun
// returns a result with Ok=false and at least one Errors entry —
// it does not surface a Go error for that case.
// Uses append-step with an invalid step name enum to trigger a CUE error.
func TestApplyDryRun_ReportsValidationErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")
	original := makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	badPayload, _ := json.Marshal(map[string]any{
		"stepId":        "STEP_10_BADSTEP",
		"name":          "BOGUS_NAME_INVALID_ENUM",
		"status":        "PENDING",
		"applicability": map[string]any{"applicable": true},
		"task":          map[string]any{},
	})
	res, err := ApplyDryRun(path, "append-step", MutatorArgs{
		Payload: badPayload,
	})
	if err != nil {
		t.Fatalf("dry-run returned Go error (expected validation in result): %v", err)
	}
	if res.Ok {
		t.Errorf("expected ok=false on bad enum value; got ok=true result=%+v", res)
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected ≥1 error string in result")
	}
	joined := strings.Join(res.Errors, "\n")
	if !strings.Contains(joined, "BOGUS") {
		t.Errorf("expected error to mention bogus value; got %q", joined)
	}
}

// TestApplyDryRun_MutationYieldsDifferentHashes (A1.4, 2026-05-05):
// a real mutation (append-step) produces before/after hashes that differ
// and the diff paths include steps-related changes.
// (Previously tested identity patch — retired in v3.0.0.)
func TestApplyDryRun_MutationYieldsDifferentHashes(t *testing.T) {
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(path, []byte(makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := ApplyDryRun(path, "append-step", MutatorArgs{
		Payload: newStepPayload(t, "STEP_10_TASK_10", "TASK_10"),
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !res.Ok {
		t.Errorf("expected ok=true; got errors=%v", res.Errors)
	}
	if res.BeforeHash == res.AfterHash {
		t.Errorf("expected before/after hashes to differ after append-step")
	}
	if len(res.DiffPaths) == 0 {
		t.Errorf("expected non-empty diffPaths; got none")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
