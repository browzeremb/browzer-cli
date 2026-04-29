package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// taskWorkflowWithScopeJSON is a workflow with a TASK step that has
// reviewer.additionalContext.changes[] populated.
func makeTaskWorkflowWithChanges(t *testing.T, scope []string, changes []map[string]any) string {
	t.Helper()
	changesAny := make([]any, len(changes))
	for i, c := range changes {
		changesAny[i] = c
	}
	scopeAny := make([]any, len(scope))
	for i, s := range scope {
		scopeAny[i] = s
	}
	taskPayload := map[string]any{
		"title": "Phase 3 Task",
		"scope": scopeAny,
		"reviewer": map[string]any{
			"additionalContext": map[string]any{
				"changes": changesAny,
			},
		},
	}
	taskBytes, err := json.Marshal(taskPayload)
	if err != nil {
		t.Fatalf("marshal task payload: %v", err)
	}
	wf := map[string]any{
		"schemaVersion":  1,
		"featureId":      "feat-reapply-test",
		"featureName":    "Reapply Test",
		"featDir":        "docs/browzer/feat-reapply-test",
		"originalRequest": "test",
		"operator":       map[string]any{"locale": "pt-BR"},
		"config":         map[string]any{"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
		"startedAt":      "2026-04-29T00:00:00Z",
		"updatedAt":      "2026-04-29T00:00:00Z",
		"totalSteps":     1,
		"completedSteps": 0,
		"notes":          []any{},
		"globalWarnings": []any{},
		"steps": []any{
			map[string]any{
				"stepId":        "STEP_05_TASK_01",
				"name":          "TASK",
				"taskId":        "TASK_01",
				"status":        "RUNNING",
				"applicability": map[string]any{"applicable": true, "reason": "default"},
				"startedAt":     "2026-04-29T00:00:00Z",
				"completedAt":   nil,
				"elapsedMin":    0,
				"retryCount":    0,
				"itDependsOn":   []any{},
				"nextStep":      "",
				"skillsToInvoke": []any{},
				"skillsInvoked": []any{},
				"owner":         nil,
				"worktrees":     map[string]any{"used": false, "worktrees": []any{}},
				"warnings":      []any{},
				"reviewHistory": []any{},
				"task":          json.RawMessage(taskBytes),
			},
		},
	}
	out, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	return string(out)
}

// TestReapplyAdditionalContext_CorrectedChangesScope verifies that
// "corrected" change entries replace the from→to in the scope array.
func TestReapplyAdditionalContext_CorrectedChangesScope(t *testing.T) {
	content := makeTaskWorkflowWithChanges(t,
		[]string{"pkg/old.go", "pkg/bar.go"},
		[]map[string]any{
			{"kind": "corrected", "from": "pkg/old.go", "to": "pkg/new.go"},
		},
	)
	wfPath := writeWorkflowFile(t, content)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "reapply-additional-context", "STEP_05_TASK_01",
		"--workflow", wfPath, "--sync",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("reapply-additional-context corrected: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	task := step["task"].(map[string]any)
	scope := task["scope"].([]any)

	if len(scope) != 2 {
		t.Fatalf("expected 2 scope entries, got %d: %v", len(scope), scope)
	}
	if scope[0] != "pkg/new.go" {
		t.Errorf("expected scope[0] = pkg/new.go after corrected, got %q", scope[0])
	}
	if scope[1] != "pkg/bar.go" {
		t.Errorf("expected scope[1] = pkg/bar.go unchanged, got %q", scope[1])
	}
}

// TestReapplyAdditionalContext_AddedAppendsToScope verifies that
// "added" change entries append new paths to the scope array.
func TestReapplyAdditionalContext_AddedAppendsToScope(t *testing.T) {
	content := makeTaskWorkflowWithChanges(t,
		[]string{"pkg/foo.go"},
		[]map[string]any{
			{"kind": "added", "path": "pkg/new.go"},
		},
	)
	wfPath := writeWorkflowFile(t, content)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "reapply-additional-context", "STEP_05_TASK_01",
		"--workflow", wfPath, "--sync",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("reapply-additional-context added: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	task := step["task"].(map[string]any)
	scope := task["scope"].([]any)

	if len(scope) != 2 {
		t.Fatalf("expected 2 scope entries after add, got %d: %v", len(scope), scope)
	}
	found := false
	for _, s := range scope {
		if s == "pkg/new.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pkg/new.go in scope after 'added' change, got: %v", scope)
	}
}

// TestReapplyAdditionalContext_DroppedRemovesFromScope verifies that
// "dropped" change entries remove paths from the scope array.
func TestReapplyAdditionalContext_DroppedRemovesFromScope(t *testing.T) {
	content := makeTaskWorkflowWithChanges(t,
		[]string{"pkg/foo.go", "pkg/remove-me.go"},
		[]map[string]any{
			{"kind": "dropped", "path": "pkg/remove-me.go"},
		},
	)
	wfPath := writeWorkflowFile(t, content)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "reapply-additional-context", "STEP_05_TASK_01",
		"--workflow", wfPath, "--sync",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("reapply-additional-context dropped: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	task := step["task"].(map[string]any)
	scope := task["scope"].([]any)

	if len(scope) != 1 {
		t.Fatalf("expected 1 scope entry after drop, got %d: %v", len(scope), scope)
	}
	if scope[0] != "pkg/foo.go" {
		t.Errorf("expected remaining scope[0] = pkg/foo.go, got %q", scope[0])
	}
}

// TestReapplyAdditionalContext_IdempotentOnSecondRun verifies that running
// reapply-additional-context twice is a no-op on the second run.
func TestReapplyAdditionalContext_IdempotentOnSecondRun(t *testing.T) {
	content := makeTaskWorkflowWithChanges(t,
		[]string{"pkg/foo.go"},
		[]map[string]any{
			{"kind": "added", "path": "pkg/bar.go"},
		},
	)
	wfPath := writeWorkflowFile(t, content)

	runCmd := func() {
		t.Helper()
		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommandT(t, &stdout, &stderr)
		root.SetArgs([]string{
			"workflow", "reapply-additional-context", "STEP_05_TASK_01",
			"--workflow", wfPath, "--sync",
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("reapply-additional-context: %v\nstderr: %s", err, stderr.String())
		}
	}

	// First run applies the change.
	runCmd()

	// Read scope after first run.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	task := step["task"].(map[string]any)
	scope1 := task["scope"].([]any)

	// Second run must be a no-op: scope length unchanged.
	runCmd()

	data2, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc2 map[string]any
	_ = json.Unmarshal(data2, &doc2)
	steps2 := doc2["steps"].([]any)
	step2 := steps2[0].(map[string]any)
	task2 := step2["task"].(map[string]any)
	scope2 := task2["scope"].([]any)

	if len(scope1) != len(scope2) {
		t.Errorf("idempotency violated: first run scope len %d, second run scope len %d",
			len(scope1), len(scope2))
	}
}
