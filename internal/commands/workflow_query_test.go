package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// queryWorkflowJSON is a minimal v1 workflow used by query CLI tests.
const queryWorkflowJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-q",
  "featureName": "Q",
  "featDir": "docs/browzer/feat-q",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_01_TASK",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 1,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_TASK",
      "name": "TASK",
      "taskId": "",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": "2026-04-29T00:01:00Z",
      "elapsedMin": 1.0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": ["execute-task"],
      "skillsInvoked": ["execute-task"],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {
        "execution": {
          "files": { "modified": ["a.ts"], "created": ["b.ts"] },
          "gates": { "postChange": { "lint": "pass", "tests": "pass" } }
        }
      }
    }
  ]
}`

// TestWorkflowQuery_KnownQueryEmitsJSON asserts a registered query name
// produces a JSON-decodable stdout.
func TestWorkflowQuery_KnownQueryEmitsJSON(t *testing.T) {
	wfPath := writeWorkflowFile(t, queryWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "query", "reused-gates", "--workflow", wfPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got []string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\nraw: %s", err, stdout.String())
	}
	if len(got) != 2 {
		t.Errorf("expected 2 reused gates (lint + tests), got %v", got)
	}
}

// TestWorkflowQuery_UnknownQueryFailsWithList asserts an unknown query name
// exits non-zero and the stderr lists the known names.
func TestWorkflowQuery_UnknownQueryFailsWithList(t *testing.T) {
	wfPath := writeWorkflowFile(t, queryWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "query", "made-up-query", "--workflow", wfPath})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for unknown query, got nil")
	}
	if !strings.Contains(stderr.String(), "unknown query") {
		t.Errorf("expected stderr to mention 'unknown query', got %q", stderr.String())
	}
	for _, expectedKnown := range []string{"reused-gates", "failed-findings", "open-deferred-actions", "task-gates-baseline"} {
		if !strings.Contains(stderr.String(), expectedKnown) {
			t.Errorf("expected stderr to list known query %q, got %q", expectedKnown, stderr.String())
		}
	}
}

// TestWorkflowQuery_AuditLineEmitted asserts the audit line is written to
// stderr in the same shape mutator verbs use (NFR-4 from the workflow design).
func TestWorkflowQuery_AuditLineEmitted(t *testing.T) {
	wfPath := writeWorkflowFile(t, queryWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "query", "changed-files", "--workflow", wfPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stderrStr := stderr.String()
	for _, marker := range []string{"verb=query", "name=changed-files", "elapsedMs=", "lockHeldMs=0", "validatedOk=true"} {
		if !strings.Contains(stderrStr, marker) {
			t.Errorf("expected audit line to contain %q, got %q", marker, stderrStr)
		}
	}
}

// TestWorkflowQuery_ChangedFilesRoundtrip asserts the changed-files query
// returns the union of modified+created across the fixture's TASK step.
func TestWorkflowQuery_ChangedFilesRoundtrip(t *testing.T) {
	wfPath := writeWorkflowFile(t, queryWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "query", "changed-files", "--workflow", wfPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got []string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode stdout: %v", err)
	}
	want := []string{"a.ts", "b.ts"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i, f := range got {
		if f != want[i] {
			t.Errorf("changed-files[%d]: expected %q, got %q", i, want[i], f)
		}
	}
}

// TestWorkflowQuery_HelpListsRegistry asserts `query --help` emits each
// registered query's name + description so authors can discover them.
func TestWorkflowQuery_HelpListsRegistry(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)
	root.SetArgs([]string{"workflow", "query", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	for _, name := range []string{
		"reused-gates",
		"failed-findings",
		"open-deferred-actions",
		"task-gates-baseline",
		"changed-files",
		"deferred-scope-adjustments",
		"open-findings",
		"next-step-id",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("query --help missing query %q (stdout: %q)", name, out)
		}
	}
}
