package workflow

import (
	"testing"
)

// TestQueryRegistry_NamesStable asserts the registry exposes every documented
// query name. A regression here breaks skill consumers — add a query, add it
// to the expected list.
func TestQueryRegistry_NamesStable(t *testing.T) {
	expected := []string{
		"tasks-manifest",
		"steps-by-name",
		"steps-by-owner",
	}
	registry := QueryRegistry()
	for _, name := range expected {
		if _, ok := registry[name]; !ok {
			t.Errorf("registry missing query %q", name)
		}
	}
	if len(registry) != len(expected) {
		t.Errorf("registry has %d queries, expected %d", len(registry), len(expected))
	}
}

// TestQueryTasksManifest returns the tasksManifest payload from the single
// TASKS_MANIFEST step, including tasksOrder + parallelizable.
func TestQueryTasksManifest(t *testing.T) {
	raw := map[string]any{
		"schemaVersion": 2,
		"steps": []any{
			map[string]any{"stepId": "STEP_01_PRD", "name": "PRD"},
			map[string]any{
				"stepId": "STEP_02_TASKS_MANIFEST",
				"name":   "TASKS_MANIFEST",
				"tasksManifest": map[string]any{
					"tasksOrder":     []any{"TASK_01", "TASK_02"},
					"parallelizable": []any{[]any{"TASK_01", "TASK_02"}},
				},
			},
		},
	}
	got, err := queryTasksManifest(raw)
	if err != nil {
		t.Fatalf("queryTasksManifest: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || m == nil {
		t.Fatalf("expected map, got %T (%v)", got, got)
	}
	if order, _ := m["tasksOrder"].([]any); len(order) != 2 {
		t.Errorf("expected 2 tasksOrder entries, got %v", m["tasksOrder"])
	}
}

// TestQueryTasksManifest_AbsentReturnsNil asserts callers can detect a missing
// TASKS_MANIFEST step via a JSON null result.
func TestQueryTasksManifest_AbsentReturnsNil(t *testing.T) {
	raw := map[string]any{"schemaVersion": 2, "steps": []any{}}
	got, err := queryTasksManifest(raw)
	if err != nil {
		t.Fatalf("queryTasksManifest: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// TestQueryStepsByOwner groups steps by their owner field in document order.
// Steps without an owner are silently skipped.
func TestQueryStepsByOwner(t *testing.T) {
	raw := map[string]any{
		"schemaVersion": 2,
		"steps": []any{
			map[string]any{"stepId": "STEP_01_TASK", "name": "TASK", "owner": "wt-A"},
			map[string]any{"stepId": "STEP_02_TASK", "name": "TASK"},
			map[string]any{"stepId": "STEP_03_TASK", "name": "TASK", "owner": "wt-A"},
			map[string]any{"stepId": "STEP_04_TASK", "name": "TASK", "owner": "wt-B"},
		},
	}
	got, err := queryStepsByOwner(raw)
	if err != nil {
		t.Fatalf("queryStepsByOwner: %v", err)
	}
	m, ok := got.(map[string][]any)
	if !ok {
		t.Fatalf("expected map[string][]any, got %T", got)
	}
	if len(m["wt-A"]) != 2 {
		t.Errorf("expected 2 wt-A steps, got %d", len(m["wt-A"]))
	}
	if len(m["wt-B"]) != 1 {
		t.Errorf("expected 1 wt-B step, got %d", len(m["wt-B"]))
	}
	if _, present := m[""]; present {
		t.Errorf("expected un-owned step to be skipped, but found empty-key bucket")
	}
}

// TestQueryStepsByName groups steps by name preserving document order so [0]
// is first and [-1] is last per group.
func TestQueryStepsByName(t *testing.T) {
	raw := map[string]any{
		"schemaVersion": 2,
		"steps": []any{
			map[string]any{"stepId": "STEP_01_BRAINSTORMING", "name": "BRAINSTORMING"},
			map[string]any{"stepId": "STEP_02_PRD", "name": "PRD"},
			map[string]any{"stepId": "STEP_03_CODE_REVIEW", "name": "CODE_REVIEW"},
			map[string]any{"stepId": "STEP_04_CODE_REVIEW", "name": "CODE_REVIEW"},
		},
	}
	got, err := queryStepsByName(raw)
	if err != nil {
		t.Fatalf("queryStepsByName: %v", err)
	}
	m, ok := got.(map[string][]any)
	if !ok {
		t.Fatalf("expected map[string][]any, got %T", got)
	}
	cr, ok := m["CODE_REVIEW"]
	if !ok || len(cr) != 2 {
		t.Fatalf("expected 2 CODE_REVIEW entries, got %v", cr)
	}
	first := stepObject(cr[0])
	last := stepObject(cr[len(cr)-1])
	if stringField(first, "stepId") != "STEP_03_CODE_REVIEW" {
		t.Errorf("first CODE_REVIEW stepId mismatch: %v", first)
	}
	if stringField(last, "stepId") != "STEP_04_CODE_REVIEW" {
		t.Errorf("last CODE_REVIEW stepId mismatch: %v", last)
	}
}
