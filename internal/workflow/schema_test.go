package workflow

import (
	"encoding/json"
	"testing"
)

// minimalTopLevel is the smallest legal schema-v1 top-level document.
const minimalTopLevel = `{
  "schemaVersion": 1,
  "featureId": "feat-test",
  "featureName": "Test Feature",
  "featDir": "docs/browzer/feat-test",
  "originalRequest": "do something",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}`

// brainstormStepJSON is a BRAINSTORMING step fixture.
const brainstormStepJSON = `{
  "stepId": "STEP_01_BRAINSTORMING",
  "name": "BRAINSTORMING",
  "status": "COMPLETED",
  "applicability": {"applicable": true, "reason": "default path"},
  "startedAt": "2026-04-29T00:00:00Z",
  "completedAt": "2026-04-29T00:01:00Z",
  "elapsedMin": 1.0,
  "retryCount": 0,
  "itDependsOn": [],
  "nextStep": "STEP_02_PRD",
  "skillsToInvoke": ["brainstorming"],
  "skillsInvoked": ["brainstorming"],
  "owner": null,
  "worktrees": {"used": false, "worktrees": []},
  "warnings": [],
  "reviewHistory": [],
  "task": {
    "questionsAsked": 3,
    "researchRoundRun": false,
    "researchAgents": 0,
    "dimensions": {
      "primaryUser": "developer",
      "jobToBeDone": "run workflows from CLI",
      "successSignal": "all tests pass",
      "inScope": ["workflow subcommands"],
      "outOfScope": ["UI"],
      "repoSurface": ["packages/cli"],
      "techConstraints": ["pure Go"],
      "failureModes": ["lock timeout"],
      "acceptanceCriteria": ["browzer workflow get-step works"],
      "dependencies": [],
      "openQuestions": []
    },
    "researchFindings": [],
    "assumptions": [],
    "openRisks": []
  }
}`

// prdStepJSON is a PRD step fixture.
const prdStepJSON = `{
  "stepId": "STEP_02_PRD",
  "name": "PRD",
  "status": "COMPLETED",
  "applicability": {"applicable": true, "reason": "default"},
  "startedAt": "2026-04-29T00:01:00Z",
  "completedAt": "2026-04-29T00:02:00Z",
  "elapsedMin": 1.0,
  "retryCount": 0,
  "itDependsOn": ["STEP_01_BRAINSTORMING"],
  "nextStep": "STEP_03_TASKS_MANIFEST",
  "skillsToInvoke": ["generate-prd"],
  "skillsInvoked": ["generate-prd"],
  "owner": null,
  "worktrees": {"used": false, "worktrees": []},
  "warnings": [],
  "reviewHistory": [],
  "task": {
    "title": "Workflow CLI subcommands",
    "overview": "add browzer workflow subcommands",
    "personas": [{"id": "P-1", "description": "developer using Browzer skills"}],
    "objectives": ["expose workflow.json via CLI"],
    "functionalRequirements": [{"id": "FR-1", "description": "get-step", "priority": "must"}],
    "nonFunctionalRequirements": [{"id": "NFR-1", "category": "perf", "description": "fast", "target": "<100ms"}],
    "successMetrics": [{"id": "M-1", "metric": "tests pass", "target": "100%", "method": "ci"}],
    "acceptanceCriteria": [{"id": "AC-1", "description": "get-step returns JSON", "bindsTo": ["FR-1"]}],
    "assumptions": [],
    "risks": [],
    "deliverables": ["workflow subcommands"],
    "inScope": ["CLI"],
    "outOfScope": ["web UI"],
    "dependencies": {"external": [], "internal": []},
    "taskGranularity": "one-task-one-commit"
  }
}`

// tasksManifestStepJSON is a TASKS_MANIFEST step fixture.
const tasksManifestStepJSON = `{
  "stepId": "STEP_03_TASKS_MANIFEST",
  "name": "TASKS_MANIFEST",
  "status": "COMPLETED",
  "applicability": {"applicable": true, "reason": "default"},
  "startedAt": "2026-04-29T00:02:00Z",
  "completedAt": "2026-04-29T00:03:00Z",
  "elapsedMin": 1.0,
  "retryCount": 0,
  "itDependsOn": ["STEP_02_PRD"],
  "nextStep": "STEP_04_TASK_01",
  "skillsToInvoke": ["generate-task"],
  "skillsInvoked": ["generate-task"],
  "owner": null,
  "worktrees": {"used": false, "worktrees": []},
  "warnings": [],
  "reviewHistory": [],
  "task": {
    "totalTasks": 2,
    "tasksOrder": ["TASK_01", "TASK_02"],
    "dependencyGraph": {"TASK_01": [], "TASK_02": ["TASK_01"]},
    "parallelizable": []
  }
}`

// taskStepJSON is a TASK step fixture with minimal task payload.
const taskStepJSON = `{
  "stepId": "STEP_04_TASK_01",
  "name": "TASK",
  "taskId": "TASK_01",
  "status": "RUNNING",
  "applicability": {"applicable": true, "reason": "default"},
  "startedAt": "2026-04-29T00:03:00Z",
  "completedAt": null,
  "elapsedMin": 0,
  "retryCount": 0,
  "itDependsOn": ["STEP_03_TASKS_MANIFEST"],
  "nextStep": "STEP_05_TASK_02",
  "skillsToInvoke": ["execute-task"],
  "skillsInvoked": [],
  "owner": "worktree-TASK_01",
  "worktrees": {"used": true, "worktrees": [{"name": "worktree-TASK_01", "status": "ACTIVE"}]},
  "warnings": [],
  "reviewHistory": [],
  "task": {
    "title": "workflow package + file resolution",
    "scope": ["packages/cli/internal/workflow/file_resolution.go"],
    "dependsOn": [],
    "invariants": [],
    "acceptanceCriteria": [{"id": "T1-AC-1", "description": "resolve path", "bindsTo": ["FR-14"]}],
    "execution": {"agents": [], "files": {"created": [], "modified": [], "deleted": []}, "gates": {}, "invariantsChecked": [], "scopeAdjustments": [], "fileEditsSummary": {}, "testsRan": {}},
    "explorer": {"skillsFound": [], "domains": [], "files": {}, "patterns": [], "notes": ""},
    "reviewer": {"testSpecs": [], "notes": ""}
  }
}`

// TestWorkflow_TopLevelRoundTrip verifies that the top-level Workflow type
// can marshal/unmarshal the minimal schema-v1 fixture without data loss.
// Covers T1-T-5: Go types round-trip every schema-v1 example.
func TestWorkflow_TopLevelRoundTrip(t *testing.T) {
	var wf Workflow
	if err := json.Unmarshal([]byte(minimalTopLevel), &wf); err != nil {
		t.Fatalf("unmarshal top-level: %v", err)
	}
	if wf.SchemaVersion != 1 {
		t.Errorf("SchemaVersion: want 1, got %d", wf.SchemaVersion)
	}
	if wf.FeatureID != "feat-test" {
		t.Errorf("FeatureID: want %q, got %q", "feat-test", wf.FeatureID)
	}
	if wf.Config.Mode != "autonomous" {
		t.Errorf("Config.Mode: want %q, got %q", "autonomous", wf.Config.Mode)
	}

	b, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wf2 Workflow
	if err := json.Unmarshal(b, &wf2); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if wf2.FeatureID != wf.FeatureID {
		t.Errorf("round-trip FeatureID mismatch: %q vs %q", wf.FeatureID, wf2.FeatureID)
	}
}

// TestWorkflow_BrainstormingStepRoundTrip verifies that a BRAINSTORMING step
// round-trips without data loss.
// Covers T1-T-5: each step type BRAINSTORMING.
func TestWorkflow_BrainstormingStepRoundTrip(t *testing.T) {
	var step Step
	if err := json.Unmarshal([]byte(brainstormStepJSON), &step); err != nil {
		t.Fatalf("unmarshal brainstorm step: %v", err)
	}
	if step.StepID != "STEP_01_BRAINSTORMING" {
		t.Errorf("StepID: want %q, got %q", "STEP_01_BRAINSTORMING", step.StepID)
	}
	if step.Name != "BRAINSTORMING" {
		t.Errorf("Name: want %q, got %q", "BRAINSTORMING", step.Name)
	}
	if step.Status != "COMPLETED" {
		t.Errorf("Status: want %q, got %q", "COMPLETED", step.Status)
	}

	b, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var step2 Step
	if err := json.Unmarshal(b, &step2); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if step2.StepID != step.StepID {
		t.Errorf("round-trip StepID mismatch")
	}
}

// TestWorkflow_PRDStepRoundTrip verifies that a PRD step round-trips.
// Covers T1-T-5: each step type PRD.
func TestWorkflow_PRDStepRoundTrip(t *testing.T) {
	var step Step
	if err := json.Unmarshal([]byte(prdStepJSON), &step); err != nil {
		t.Fatalf("unmarshal PRD step: %v", err)
	}
	if step.Name != "PRD" {
		t.Errorf("Name: want %q, got %q", "PRD", step.Name)
	}
	b, _ := json.Marshal(step)
	var step2 Step
	if err := json.Unmarshal(b, &step2); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if step2.Name != "PRD" {
		t.Errorf("round-trip Name mismatch")
	}
}

// TestWorkflow_TasksManifestStepRoundTrip verifies that a TASKS_MANIFEST step round-trips.
// Covers T1-T-5: each step type TASKS_MANIFEST.
func TestWorkflow_TasksManifestStepRoundTrip(t *testing.T) {
	var step Step
	if err := json.Unmarshal([]byte(tasksManifestStepJSON), &step); err != nil {
		t.Fatalf("unmarshal TASKS_MANIFEST step: %v", err)
	}
	if step.Name != "TASKS_MANIFEST" {
		t.Errorf("Name: want %q, got %q", "TASKS_MANIFEST", step.Name)
	}
	b, _ := json.Marshal(step)
	var step2 Step
	if err := json.Unmarshal(b, &step2); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if step2.Name != "TASKS_MANIFEST" {
		t.Errorf("round-trip Name mismatch")
	}
}

// TestWorkflow_TaskStepRoundTrip verifies that a TASK step round-trips without
// data loss and preserves taskId.
// Covers T1-T-5: each step type TASK.
func TestWorkflow_TaskStepRoundTrip(t *testing.T) {
	var step Step
	if err := json.Unmarshal([]byte(taskStepJSON), &step); err != nil {
		t.Fatalf("unmarshal TASK step: %v", err)
	}
	if step.Name != "TASK" {
		t.Errorf("Name: want %q, got %q", "TASK", step.Name)
	}
	if step.TaskID != "TASK_01" {
		t.Errorf("TaskID: want %q, got %q", "TASK_01", step.TaskID)
	}
	b, _ := json.Marshal(step)
	var step2 Step
	if err := json.Unmarshal(b, &step2); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if step2.TaskID != "TASK_01" {
		t.Errorf("round-trip TaskID mismatch")
	}
}

// TestWorkflow_AllLifecycleStatuses verifies that every legal lifecycle status
// value can be stored in a Step without data loss.
// Covers T1-T-5: each lifecycle status.
func TestWorkflow_AllLifecycleStatuses(t *testing.T) {
	statuses := []string{
		"PENDING", "RUNNING", "AWAITING_REVIEW", "COMPLETED",
		"PAUSED_PENDING_OPERATOR", "SKIPPED", "STOPPED",
	}
	for _, s := range statuses {
		t.Run(s, func(t *testing.T) {
			step := Step{
				StepID: "STEP_01",
				Name:   "BRAINSTORMING",
				Status: StepStatus(s),
			}
			b, err := json.Marshal(step)
			if err != nil {
				t.Fatalf("marshal %q: %v", s, err)
			}
			var step2 Step
			if err := json.Unmarshal(b, &step2); err != nil {
				t.Fatalf("unmarshal %q: %v", s, err)
			}
			if string(step2.Status) != s {
				t.Errorf("status round-trip: want %q, got %q", s, step2.Status)
			}
		})
	}
}
