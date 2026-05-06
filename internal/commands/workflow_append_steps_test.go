package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// stepBatchPayloadJSON is a JSON array of three minimal-but-valid step
// objects used in append-steps happy-path tests.
const stepBatchPayloadJSON = `[
  {
    "stepId": "STEP_03_TASKS_MANIFEST",
    "name": "TASKS_MANIFEST",
    "taskId": "",
    "status": "PENDING",
    "applicability": {"applicable": true, "reason": "default"},
    "startedAt": "",
    "completedAt": null,
    "elapsedMin": 0,
    "retryCount": 0,
    "itDependsOn": [],
    "nextStep": "",
    "skillsToInvoke": [],
    "skillsInvoked": [],
    "owner": null,
    "worktrees": {"used": false, "worktrees": []},
    "warnings": [],
    "reviewHistory": [],
    "task": {}
  },
  {
    "stepId": "STEP_04_TASK_01",
    "name": "TASK",
    "taskId": "TASK_01",
    "status": "PENDING",
    "applicability": {"applicable": true, "reason": "default"},
    "startedAt": "",
    "completedAt": null,
    "elapsedMin": 0,
    "retryCount": 0,
    "itDependsOn": [],
    "nextStep": "",
    "skillsToInvoke": [],
    "skillsInvoked": [],
    "owner": null,
    "worktrees": {"used": false, "worktrees": []},
    "warnings": [],
    "reviewHistory": [],
    "task": {}
  },
  {
    "stepId": "STEP_05_TASK_02",
    "name": "TASK",
    "taskId": "TASK_02",
    "status": "PENDING",
    "applicability": {"applicable": true, "reason": "default"},
    "startedAt": "",
    "completedAt": null,
    "elapsedMin": 0,
    "retryCount": 0,
    "itDependsOn": [],
    "nextStep": "",
    "skillsToInvoke": [],
    "skillsInvoked": [],
    "owner": null,
    "worktrees": {"used": false, "worktrees": []},
    "warnings": [],
    "reviewHistory": [],
    "task": {}
  }
]`

// TestAppendSteps_HappyPath_3StepsAppended asserts the canonical happy
// path: a 3-element JSON array is appended to workflow.json in a single
// dispatch, with totalSteps recomputed correctly.
func TestAppendSteps_HappyPath_3StepsAppended(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	payloadFile := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(payloadFile, []byte(stepBatchPayloadJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-steps",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("append-steps happy path: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(doc.Steps))
	}
	wantIDs := []string{"STEP_03_TASKS_MANIFEST", "STEP_04_TASK_01", "STEP_05_TASK_02"}
	for i, want := range wantIDs {
		if doc.Steps[i].StepID != want {
			t.Errorf("step[%d] stepId=%q, want %q", i, doc.Steps[i].StepID, want)
		}
	}
	if doc.TotalSteps != 3 {
		t.Errorf("totalSteps=%d, want 3", doc.TotalSteps)
	}
}

// TestAppendSteps_EmptyArrayErrors asserts that an empty array `[]` is
// rejected — callers building the array from templates should fail
// loudly when the template expanded to zero entries instead of writing
// a no-op.
func TestAppendSteps_EmptyArrayErrors(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	payloadFile := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(payloadFile, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-steps",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err == nil {
		t.Fatal("expected error for empty payload array")
	}

	after, _ := os.ReadFile(wfPath)
	if string(before) != string(after) {
		t.Error("empty-array append-steps must not mutate workflow.json")
	}
}

// TestAppendSteps_PayloadMustBeJSONArray asserts a single object payload
// (the singular append-step shape) is rejected with a clear error so
// operators don't silently get a one-element batch when they meant to
// use the singular verb.
func TestAppendSteps_PayloadMustBeJSONArray(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	// Single object, not wrapped in an array.
	payload := `{"stepId":"STEP_02_PRD","name":"PRD","status":"PENDING"}`
	payloadFile := filepath.Join(t.TempDir(), "object.json")
	if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-steps",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err == nil {
		t.Fatal("expected error when payload is a JSON object, not an array")
	}
}

// TestAppendSteps_ElementMustBeObject asserts that an array containing a
// non-object element (e.g. a string or number) is rejected with an
// indexed error — the error message must name the offending position so
// the operator can fix the right entry.
func TestAppendSteps_ElementMustBeObject(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	// Array with a string at index 1.
	payload := `[
		{"stepId":"STEP_X","name":"PRD","status":"PENDING"},
		"not-an-object",
		{"stepId":"STEP_Y","name":"TASK","status":"PENDING"}
	]`
	payloadFile := filepath.Join(t.TempDir(), "mixed.json")
	if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-steps",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Fatal("expected error when array contains non-object element")
	}
	combined := stdout.String() + stderr.String() + err.Error()
	if !strings.Contains(combined, "[1]") {
		t.Errorf("expected error to reference offending index [1], got: %s", combined)
	}

	after, _ := os.ReadFile(wfPath)
	if string(before) != string(after) {
		t.Error("malformed batch must not mutate workflow.json (atomic rollback)")
	}
}

// TestAppendSteps_ElementMustHaveStepId asserts pre-mutation validation
// rejects entries that are well-typed objects but lack a non-empty string
// stepId. Closes RETRO §I4: without this gate, the audit-line emitted on
// failure could lie (claiming an earlier element's stepId as the "last
// appended" when in fact a later element with no stepId is what failed CUE).
func TestAppendSteps_ElementMustHaveStepId(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		payload string
		wantIdx string
	}{
		{
			name: "missing-stepid-field",
			payload: `[
				{"stepId":"STEP_OK","name":"PRD","status":"PENDING"},
				{"name":"TASK","status":"PENDING"}
			]`,
			wantIdx: "[1]",
		},
		{
			name: "empty-stepid-string",
			payload: `[
				{"stepId":"","name":"PRD","status":"PENDING"}
			]`,
			wantIdx: "[0]",
		},
		{
			name: "non-string-stepid",
			payload: `[
				{"stepId":42,"name":"PRD","status":"PENDING"}
			]`,
			wantIdx: "[0]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payloadFile := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(payloadFile, []byte(tc.payload), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			root := buildWorkflowCommandT(t, &stdout, &stderr)
			root.SetArgs([]string{"workflow", "append-steps", "--payload", payloadFile, "--workflow", wfPath})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected error when payload entry lacks valid stepId")
			}
			combined := stdout.String() + stderr.String() + err.Error()
			if !strings.Contains(combined, tc.wantIdx) {
				t.Errorf("expected error to reference offending index %s, got: %s", tc.wantIdx, combined)
			}
			if !strings.Contains(combined, "stepId") {
				t.Errorf("expected error to mention 'stepId', got: %s", combined)
			}
			after, _ := os.ReadFile(wfPath)
			if string(before) != string(after) {
				t.Error("invalid batch must not mutate workflow.json (atomic rollback)")
			}
		})
	}
}

// TestAppendSteps_SingleAuditLineEmitted asserts the batch produces ONE
// audit line on success — confirming the advisory lock is acquired once,
// the CUE validator runs once, and the persistence is one tmp+rename.
// Closes the contract from RETRO 9.1.5: the value of the plural verb is
// proportional to the lock-coalescing it enables.
func TestAppendSteps_SingleAuditLineEmitted(t *testing.T) {
	t.Setenv("BROWZER_LLM", "")
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	payloadFile := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(payloadFile, []byte(stepBatchPayloadJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-steps",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("append-steps: %v\nstderr: %s", err, stderr.String())
	}

	count := strings.Count(stderr.String(), "verb=append-steps")
	if count != 1 {
		t.Errorf("expected exactly 1 audit line, got %d\nstderr: %s", count, stderr.String())
	}
}

// TestAppendSteps_QuietFlagSuppressesAuditLine mirrors the singular
// verb's --quiet contract.
func TestAppendSteps_QuietFlagSuppressesAuditLine(t *testing.T) {
	t.Setenv("BROWZER_LLM", "")
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	payloadFile := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(payloadFile, []byte(stepBatchPayloadJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-steps",
		"--payload", payloadFile,
		"--workflow", wfPath,
		"--quiet",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("append-steps --quiet: %v", err)
	}
	if strings.Contains(stderr.String(), "verb=append-steps") {
		t.Errorf("--quiet must suppress audit line, got stderr: %q", stderr.String())
	}
}

// TestMutatorAppendSteps_DirectInvariants exercises the mutator directly
// against an in-memory map — bypasses the CLI dispatch + lock + CUE pass
// so the unit-level invariants (counter recompute, last-stepId tracking,
// bad-payload errors) are pinned without file IO.
func TestMutatorAppendSteps_DirectInvariants(t *testing.T) {
	t.Run("happy path sets out.StepID to last entry", func(t *testing.T) {
		raw := map[string]any{
			"steps":          []any{},
			"totalSteps":     float64(0),
			"completedSteps": float64(0),
			"updatedAt":      "2026-04-29T00:00:00Z",
		}
		args := wf.MutatorArgs{Payload: []byte(`[
			{"stepId":"A","status":"PENDING"},
			{"stepId":"B","status":"PENDING"},
			{"stepId":"C","status":"PENDING"}
		]`)}
		var out wf.ApplyResult
		if err := wf.Mutators["append-steps"](raw, args, &out); err != nil {
			t.Fatalf("mutator error: %v", err)
		}
		if out.StepID != "C" {
			t.Errorf("expected StepID=C (last), got %q", out.StepID)
		}
		steps, _ := raw["steps"].([]any)
		if len(steps) != 3 {
			t.Errorf("expected 3 steps, got %d", len(steps))
		}
	})

	t.Run("empty payload errors", func(t *testing.T) {
		raw := map[string]any{"steps": []any{}}
		var out wf.ApplyResult
		err := wf.Mutators["append-steps"](raw, wf.MutatorArgs{}, &out)
		if err == nil {
			t.Fatal("expected error for empty payload")
		}
		if !strings.Contains(err.Error(), "required") {
			t.Errorf("error should mention 'required', got: %v", err)
		}
	})

	t.Run("non-array JSON errors", func(t *testing.T) {
		raw := map[string]any{"steps": []any{}}
		args := wf.MutatorArgs{Payload: []byte(`{"stepId":"X"}`)}
		var out wf.ApplyResult
		err := wf.Mutators["append-steps"](raw, args, &out)
		if err == nil {
			t.Fatal("expected error for non-array payload")
		}
	})

	t.Run("empty array errors", func(t *testing.T) {
		raw := map[string]any{"steps": []any{}}
		args := wf.MutatorArgs{Payload: []byte(`[]`)}
		var out wf.ApplyResult
		err := wf.Mutators["append-steps"](raw, args, &out)
		if err == nil {
			t.Fatal("expected error for empty array payload")
		}
		if !strings.Contains(err.Error(), "no-op") {
			t.Errorf("error should explain it's a no-op, got: %v", err)
		}
	})

	t.Run("non-object element errors with index", func(t *testing.T) {
		raw := map[string]any{"steps": []any{}}
		args := wf.MutatorArgs{Payload: []byte(`[{"stepId":"A"}, 42, {"stepId":"C"}]`)}
		var out wf.ApplyResult
		err := wf.Mutators["append-steps"](raw, args, &out)
		if err == nil {
			t.Fatal("expected error when element 1 is not an object")
		}
		if !strings.Contains(err.Error(), "[1]") {
			t.Errorf("error should name index [1], got: %v", err)
		}
	})
}

// TestAppendSteps_RegisteredInMutators is a structural pin that catches
// the case where the cobra command exists but the verb is missing from
// the Mutators registry (or vice-versa). Without it, the daemon path
// would silently 404 with `unknown_verb` while the standalone path
// worked, producing a confusing fallback-only behaviour.
func TestAppendSteps_RegisteredInMutators(t *testing.T) {
	if _, ok := wf.Mutators["append-steps"]; !ok {
		t.Fatal("append-steps verb missing from wf.Mutators registry — daemon path will return unknown_verb")
	}
}
