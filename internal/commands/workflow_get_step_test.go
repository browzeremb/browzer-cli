package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflowWithStepsJSON is a minimal workflow.json with two steps, used in
// get-step tests.
const workflowWithStepsJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-test",
  "featureName": "Test Feature",
  "featDir": "docs/browzer/feat-test",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_01_BRAINSTORMING",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_BRAINSTORMING",
      "name": "BRAINSTORMING",
      "taskId": "",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": "2026-04-29T00:01:00Z",
      "elapsedMin": 1.0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": ["brainstorming"],
      "skillsInvoked": ["brainstorming"],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {
        "questionsAsked": 5,
        "researchRoundRun": false,
        "researchAgents": 0,
        "dimensions": {
          "primaryUser": "developer",
          "jobToBeDone": "test",
          "successSignal": "passes",
          "inScope": [],
          "outOfScope": [],
          "repoSurface": [],
          "techConstraints": [],
          "failureModes": [],
          "acceptanceCriteria": [],
          "dependencies": [],
          "openQuestions": []
        },
        "researchFindings": [],
        "assumptions": [],
        "openRisks": []
      }
    }
  ]
}`

// writeWorkflowFile writes a workflow JSON fixture to a temp dir and
// returns the file path.
func writeWorkflowFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writeWorkflowFile: %v", err)
	}
	return wfPath
}

// TestGetStep_ExistingStepReturnsFullJSON verifies that
// `browzer workflow get-step STEP_01_BRAINSTORMING` returns the full step
// JSON on stdout and exits 0.
// Covers T1-T-r1: get-step on existing step returns full JSON on stdout.
func TestGetStep_ExistingStepReturnsFullJSON(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-step", "STEP_01_BRAINSTORMING", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if out == "" {
		t.Fatal("expected JSON output on stdout, got empty")
	}

	// Output must be valid JSON containing the stepId.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, out)
	}
	if parsed["stepId"] != "STEP_01_BRAINSTORMING" {
		t.Errorf("expected stepId %q in output, got %v", "STEP_01_BRAINSTORMING", parsed["stepId"])
	}
}

// TestGetStep_MissingStepExitsNonZeroWithMessage verifies that requesting a
// step ID that doesn't exist exits non-zero and prints a descriptive message
// on stderr naming the step.
// Covers T1-T-r1: missing step exits non-zero with stderr message naming the step.
func TestGetStep_MissingStepExitsNonZeroWithMessage(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-step", "STEP_99_NONEXISTENT", "--workflow", wfPath})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for missing step, got nil error")
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "STEP_99_NONEXISTENT") {
		t.Errorf("expected stderr to name the missing stepId, got %q", stderrStr)
	}
}

// TestGetStep_FieldScalarUnquoted verifies that --field brainstorm.successSignal
// returns the unquoted scalar value.
// Covers T1-T-r2: --field brainstorm.successSignal returns unquoted scalar.
func TestGetStep_FieldScalarUnquoted(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--field", "task.dimensions.successSignal",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out != "passes" {
		t.Errorf("expected unquoted scalar %q, got %q", "passes", out)
	}
	if strings.HasPrefix(out, `"`) {
		t.Errorf("scalar should not be quoted, got %q", out)
	}
}

// TestGetStep_FieldObjectAsJSON verifies that --field task (an object) returns
// its JSON representation.
// Covers T1-T-r2: --field brainstorm returns object as JSON.
func TestGetStep_FieldObjectAsJSON(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--field", "task",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("expected valid JSON for object field, got %q: %v", out, err)
	}
}

// TestGetStep_FieldNonexistentExitsNonZero verifies that --field for a path
// that doesn't exist exits non-zero.
// Covers T1-T-r2: --field nonexistent.path exits non-zero.
func TestGetStep_FieldNonexistentExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--field", "nonexistent.deep.path",
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for nonexistent field, got nil error")
	}
}

// workflowWithTaskStepJSON is a workflow fixture containing a TASK step with
// the fields consumed by the execute-task render template.
const workflowWithTaskStepJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-render-test",
  "featureName": "Render Test",
  "featDir": "docs/browzer/feat-render-test",
  "originalRequest": "test render",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_04_TASK_01",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_04_TASK_01",
      "name": "TASK",
      "taskId": "TASK_01",
      "status": "RUNNING",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": ["execute-task"],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {
        "title": "Implement render flag",
        "scope": ["packages/cli/internal/workflow/render.go", "packages/cli/internal/commands/workflow_get_step.go"],
        "suggestedModel": "sonnet",
        "trivial": false,
        "invariants": [
          {"rule": "no external deps", "source": "PRD"}
        ],
        "reviewer": {
          "tddDecision": {
            "applicable": true,
            "reason": "clear I/O behavior"
          },
          "testSpecs": [
            {"type": "red", "description": "all fields present"},
            {"type": "green", "description": "minimal step no panic"}
          ]
        },
        "explorer": {
          "skillsFound": [
            {"skill": "go-best-practices", "domain": "go"},
            {"skill": "superpowers:tdd", "domain": "testing"}
          ]
        }
      }
    }
  ]
}`

// TestGetStep_RenderExecuteTaskReturnsFormattedBlock verifies that
// --render execute-task on a TASK step prints a formatted block with
// the expected field labels on stdout.
func TestGetStep_RenderExecuteTaskReturnsFormattedBlock(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--render", "execute-task",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v (stderr: %s)", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{"Title:", "Scope:", "TDD applicable:", "Skills:", "Invariants:", "Suggested model:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\nfull output:\n%s", want, out)
		}
	}
}

// TestGetStep_RenderUnknownTemplateExitsNonZero verifies that --render bogus
// exits non-zero and prints a message naming the unknown template on stderr.
func TestGetStep_RenderUnknownTemplateExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--render", "bogus",
	})

	if err := root.Execute(); err == nil {
		t.Fatal("expected non-zero exit for unknown template, got nil")
	}
	if !strings.Contains(stderr.String(), "bogus") {
		t.Errorf("expected stderr to name unknown template %q, got: %s", "bogus", stderr.String())
	}
}

// TestGetStep_RenderConflictsWithField verifies that combining --render and
// --field exits non-zero (mutually exclusive flags).
func TestGetStep_RenderConflictsWithField(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--render", "execute-task",
		"--field", "task.title",
	})

	if err := root.Execute(); err == nil {
		t.Fatal("expected non-zero exit when --render and --field are combined, got nil")
	}
}

// workflowWithPRDStepJSON is a workflow fixture containing a PRD step.
const workflowWithPRDStepJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-prd-test",
  "featureName": "PRD Test",
  "featDir": "docs/browzer/feat-prd-test",
  "originalRequest": "test prd",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_02_PRD",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_02_PRD",
      "name": "PRD",
      "taskId": "",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": "2026-04-29T00:10:00Z",
      "elapsedMin": 10.0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "STEP_03_TASKS_MANIFEST",
      "skillsToInvoke": ["generate-prd"],
      "skillsInvoked": ["generate-prd"],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {
        "title": "Browzer CLI Workflow Subcommands",
        "summary": "Add workflow subcommands to the CLI",
        "functionalRequirements": [
          {"id": "FR-1", "title": "get-step read verb"},
          {"id": "FR-2", "title": "append-step write verb"},
          {"id": "FR-3", "title": "set-status lifecycle"}
        ],
        "acceptanceCriteria": [
          {"id": "AC-1", "description": "get-step returns full JSON"},
          {"id": "AC-2", "description": "get-step --field returns scalar"}
        ],
        "nonFunctionalRequirements": [
          {"id": "NFR-1", "description": "no new external deps"}
        ]
      }
    }
  ]
}`

// workflowWithTaskStepWithQuotesJSON is a TASK step fixture with single quotes
// in the title. The JSON value uses the JSON string escape (no special escaping
// needed — single quotes are valid JSON string content).
const workflowWithTaskStepWithQuotesJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-quotes-test",
  "featureName": "Quotes Test",
  "featDir": "docs/browzer/feat-quotes-test",
  "originalRequest": "test quotes",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_04_TASK_01",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_04_TASK_01",
      "name": "TASK",
      "taskId": "TASK_01",
      "status": "RUNNING",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": ["execute-task"],
      "skillsInvoked": [],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {
        "title": "It's a task with single quotes: don't break",
        "scope": ["packages/cli/internal/commands/workflow_get_step.go"],
        "suggestedModel": "sonnet",
        "trivial": false,
        "invariants": [],
        "reviewer": {
          "tddDecision": {"applicable": true, "reason": "clear I/O"},
          "testSpecs": []
        },
        "explorer": {"skillsFound": []}
      }
    }
  ]
}`

// TestGetStep_BashVars_TaskStepEmitsExpectedKeys verifies that --bash-vars on a
// TASK step emits all expected key=value lines including step_id, task_title,
// task_trivial, and task_tdd_applicable.
func TestGetStep_BashVars_TaskStepEmitsExpectedKeys(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--bash-vars",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v (stderr: %s)", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"step_id=",
		"step_name=",
		"step_status=",
		"task_title=",
		"task_trivial=",
		"task_tdd_applicable=",
		"task_suggested_model=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\nfull output:\n%s", want, out)
		}
	}
}

// TestGetStep_BashVars_PrdStepEmitsPrdKeys verifies that --bash-vars on a PRD
// step emits prd_title= etc. and does NOT contain task-specific keys.
func TestGetStep_BashVars_PrdStepEmitsPrdKeys(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithPRDStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_02_PRD",
		"--workflow", wfPath,
		"--bash-vars",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v (stderr: %s)", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{"step_id=", "prd_title=", "prd_total_frs=", "prd_total_acs=", "prd_total_nfrs="} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\nfull output:\n%s", want, out)
		}
	}
	// Must not contain TASK-specific keys
	for _, notWant := range []string{"task_title=", "task_trivial=", "task_tdd_applicable="} {
		if strings.Contains(out, notWant) {
			t.Errorf("expected output NOT to contain TASK key %q for PRD step\nfull output:\n%s", notWant, out)
		}
	}
}

// TestGetStep_BashVars_ConflictsWithField verifies that --bash-vars --field x
// exits non-zero (mutually exclusive).
func TestGetStep_BashVars_ConflictsWithField(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--bash-vars",
		"--field", "task.title",
	})

	if err := root.Execute(); err == nil {
		t.Fatal("expected non-zero exit when --bash-vars and --field are combined, got nil")
	}
}

// TestGetStep_BashVars_ConflictsWithJson verifies that --bash-vars --json
// exits non-zero (mutually exclusive).
func TestGetStep_BashVars_ConflictsWithJson(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--bash-vars",
		"--json",
	})

	if err := root.Execute(); err == nil {
		t.Fatal("expected non-zero exit when --bash-vars and --json are combined, got nil")
	}
}

// TestGetStep_BashVars_ConflictsWithRender verifies that --bash-vars --render
// execute-task exits non-zero (mutually exclusive).
func TestGetStep_BashVars_ConflictsWithRender(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--bash-vars",
		"--render", "execute-task",
	})

	if err := root.Execute(); err == nil {
		t.Fatal("expected non-zero exit when --bash-vars and --render are combined, got nil")
	}
}

// TestGetStep_BashVars_OutputIsEvalSafe verifies that single quotes in string
// values are escaped via the '\''-idiom so output is eval-safe, and that
// the escaped value round-trips correctly.
func TestGetStep_BashVars_OutputIsEvalSafe(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithTaskStepWithQuotesJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_04_TASK_01",
		"--workflow", wfPath,
		"--bash-vars",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v (stderr: %s)", err, stderr.String())
	}

	out := stdout.String()
	// The title contains single quotes; they must be escaped via '\''
	if !strings.Contains(out, `'\''`) {
		t.Errorf("expected output to contain single-quote escape sequence '\\''\\''\\'' but got:\n%s", out)
	}
	// The raw title value must appear in the output (split across escape sequences)
	if !strings.Contains(out, "It") || !strings.Contains(out, "s a task with single quotes") {
		t.Errorf("title content not found in output:\n%s", out)
	}
}

// TestGetStep_FieldScalarJSONMode verifies that --field <scalar-path> --json
// wraps the scalar as JSON (string in quotes, number as JSON literal).
// Covers T1-T-r3: --field <scalar-path> --json wraps scalar in JSON.
func TestGetStep_FieldScalarJSONMode(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	t.Run("string-scalar", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		root.SetArgs([]string{
			"workflow", "get-step", "STEP_01_BRAINSTORMING",
			"--field", "task.dimensions.successSignal",
			"--json",
			"--workflow", wfPath,
		})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}

		out := strings.TrimSpace(stdout.String())
		// In --json mode, string scalar must be JSON-encoded: "passes"
		if out != `"passes"` {
			t.Errorf("expected JSON-encoded string %q, got %q", `"passes"`, out)
		}
	})

	t.Run("number-scalar", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommand(&stdout, &stderr)
		root.SetArgs([]string{
			"workflow", "get-step", "STEP_01_BRAINSTORMING",
			"--field", "task.questionsAsked",
			"--json",
			"--workflow", wfPath,
		})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}

		out := strings.TrimSpace(stdout.String())
		// Must be a JSON literal (parseable as JSON number).
		var n any
		if err := json.Unmarshal([]byte(out), &n); err != nil {
			t.Errorf("expected JSON literal for number, got %q: %v", out, err)
		}
	})
}
