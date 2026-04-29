package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// minimalStepPayload is a valid step JSON payload for append-step tests.
const minimalStepPayload = `{
  "stepId": "STEP_02_PRD",
  "name": "PRD",
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
}`

// TestAppendStep_ValidPayloadAppendsStepAndRecomputes verifies that
// `browzer workflow append-step --payload <file>` appends the step and
// recomputes totalSteps/completedSteps/updatedAt.
// Covers T3-T-1.
func TestAppendStep_ValidPayloadAppendsStepAndRecomputes(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	// Write the payload to a temp file.
	payloadFile := filepath.Join(t.TempDir(), "step.json")
	if err := os.WriteFile(payloadFile, []byte(minimalStepPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-step",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("append-step with valid payload should exit 0, got error: %v\nstderr: %s", err, stderr.String())
	}

	// Read back the mutated file and verify.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("read back workflow: %v", err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after append: %v", err)
	}

	if len(doc.Steps) != 1 {
		t.Fatalf("expected 1 step after append, got %d", len(doc.Steps))
	}
	if doc.Steps[0].StepID != "STEP_02_PRD" {
		t.Errorf("expected appended step STEP_02_PRD, got %q", doc.Steps[0].StepID)
	}
	if doc.TotalSteps != 1 {
		t.Errorf("expected totalSteps=1 after append, got %d", doc.TotalSteps)
	}
}

// TestAppendStep_InvalidPayloadExitsNonZeroWithNoMutation verifies that
// an invalid step payload (e.g. illegal status) exits non-zero and leaves
// the workflow.json file byte-for-byte unchanged.
// Covers T3-T-1.
func TestAppendStep_InvalidPayloadExitsNonZeroWithNoMutation(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	// Payload with illegal status value.
	invalidPayload := `{
  "stepId": "STEP_02_BAD",
  "name": "PRD",
  "status": "ILLEGAL_STATUS_VALUE",
  "applicability": {"applicable": true, "reason": "default"},
  "completedAt": null,
  "owner": null,
  "worktrees": {"used": false, "worktrees": []},
  "task": {}
}`
	payloadFile := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(payloadFile, []byte(invalidPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-step",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for invalid payload, got nil error")
	}

	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("append-step with invalid payload must not mutate workflow.json")
	}
}

// TestAppendStep_LockContentionExitsCode16 verifies that when the advisory
// lock is held by another goroutine, append-step with --lock-timeout=100ms
// exits with the lock-timeout error (exit code 16 path).
// Covers T3-T-9.
func TestAppendStep_LockContentionExitsCode16(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	// Hold the lock with a goroutine for the duration of the test.
	lock, err := wf.NewLock(wfPath, 5*time.Second, os.Stderr)
	if err != nil {
		t.Fatalf("NewLock: %v", err)
	}
	if err := lock.Acquire(); err != nil {
		t.Fatalf("Acquire lock: %v", err)
	}
	defer lock.Release() //nolint:errcheck

	payloadFile := filepath.Join(t.TempDir(), "step.json")
	if err := os.WriteFile(payloadFile, []byte(minimalStepPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-step",
		"--payload", payloadFile,
		"--lock-timeout", "100ms",
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected error from append-step under lock contention, got nil")
	}

	// AC-16 (FR-16): the error MUST carry exit code 16.
	// Unwrap to find a *cliErrors.CliError with ExitCode == 16.
	var cliErr *cliErrors.CliError
	if !errors.As(err, &cliErr) {
		t.Errorf("expected *cliErrors.CliError, got %T: %v", err, err)
	} else if cliErr.ExitCode != 16 {
		t.Errorf("expected exit code 16 for lock contention, got %d (error: %v)", cliErr.ExitCode, err)
	}

	// The error must also wrap wf.ErrLockTimeout for semantic consistency.
	if !errors.Is(err, wf.ErrLockTimeout) {
		// Also acceptable: the error message contains "lock".
		if !strings.Contains(err.Error(), "lock") {
			t.Errorf("expected lock timeout error, got: %v", err)
		}
	}

	// Stderr should mention lock contention.
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, wfPath) && !strings.Contains(stderrStr, "lock") {
		t.Errorf("expected stderr to mention lock contention, got: %q", stderrStr)
	}
}

// TestAppendStep_NoLockBypassEmitsWarningAndMutates verifies that
// --no-lock bypasses the advisory lock and still mutates the file, but
// emits a warning on stderr.
// Covers T3-T-10.
func TestAppendStep_NoLockBypassEmitsWarningAndMutates(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	payloadFile := filepath.Join(t.TempDir(), "step.json")
	if err := os.WriteFile(payloadFile, []byte(minimalStepPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-step",
		"--payload", payloadFile,
		"--no-lock",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("append-step --no-lock should succeed, got: %v\nstderr: %s", err, stderr.String())
	}

	// Mutation must have happened.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after no-lock append: %v", err)
	}
	if len(doc.Steps) != 1 {
		t.Errorf("expected 1 step after --no-lock append, got %d", len(doc.Steps))
	}

	// Warning must be on stderr.
	stderrStr := stderr.String()
	if !strings.Contains(strings.ToLower(stderrStr), "lock") {
		t.Errorf("expected --no-lock warning on stderr, got: %q", stderrStr)
	}
}

// TestAppendStep_ConcurrencyN8NoLostWrites is the concurrency contract test:
// N=8 goroutines each append a distinct step to the same workflow.json.
// The final file must contain exactly 8 steps with consistent
// totalSteps/completedSteps counters and no corruption.
// Covers T3-T-11.
func TestAppendStep_ConcurrencyN8NoLostWrites(t *testing.T) {
	// t.Setenv is forbidden from the goroutines spawned below — set the
	// dispatch-mode env once at the parent test boundary so every spawned
	// cobra root inherits it and resolves to writeModeStandalone via
	// resolveWriteMode's BROWZER_WORKFLOW_MODE branch.
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")

	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	const N = 8

	type result struct {
		stepN int
		err   error
	}
	results := make([]result, N)
	var wg sync.WaitGroup

	for i := range N {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			// Each goroutine gets its own cobra command + buffers.
			// NOTE: buildWorkflowCommand (not …T) — t.Setenv is unsafe from
			// spawned goroutines. The env was set at the parent test above.
			var stdout, stderr bytes.Buffer
			root := buildWorkflowCommand(&stdout, &stderr)

			// Build a unique valid step payload.
			stepID := fmt.Sprintf("STEP_%02d_TASK", n+2)
			payload := fmt.Sprintf(`{
  "stepId": %q,
  "name": "TASK",
  "taskId": "TASK_%02d",
  "status": "PENDING",
  "applicability": {"applicable": true, "reason": "default"},
  "completedAt": null,
  "owner": null,
  "worktrees": {"used": false, "worktrees": []},
  "task": {}
}`, stepID, n+2)

			payloadFile := filepath.Join(t.TempDir(), fmt.Sprintf("step_%d.json", n))
			if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
				results[n] = result{n, err}
				return
			}

			root.SetArgs([]string{
				"workflow", "append-step",
				"--payload", payloadFile,
				"--workflow", wfPath,
			})

			err := root.Execute()
			results[n] = result{n, err}
		}(i)
	}

	wg.Wait()

	// All goroutines must have succeeded.
	for i, r := range results {
		if r.err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, r.err)
		}
	}

	// Read back and verify.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("read back workflow: %v", err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after concurrent appends: %v", err)
	}

	if len(doc.Steps) != N {
		t.Errorf("expected %d steps after N=%d concurrent appends, got %d", N, N, len(doc.Steps))
	}
	if doc.TotalSteps != N {
		t.Errorf("expected totalSteps=%d, got %d", N, doc.TotalSteps)
	}

	// No two steps may share the same stepId (no lost-write corruption).
	seen := make(map[string]int)
	for _, s := range doc.Steps {
		seen[s.StepID]++
		if seen[s.StepID] > 1 {
			t.Errorf("duplicate stepId %q in final workflow — write was lost/corrupted", s.StepID)
		}
	}
}

// TestAppendStep_AuditLineOnStderr verifies that a successful append-step
// emits a structured audit line to stderr containing:
//
//	verb=append-step stepId=<id> lockHeldMs=<n> validatedOk=<bool>
//
// Covers T3-T-12.
func TestAppendStep_AuditLineOnStderr(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	payloadFile := filepath.Join(t.TempDir(), "step.json")
	if err := os.WriteFile(payloadFile, []byte(minimalStepPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-step",
		"--payload", payloadFile,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("append-step: %v\nstderr: %s", err, stderr.String())
	}

	stderrStr := stderr.String()
	required := []string{"verb=append-step", "stepId=", "lockHeldMs=", "validatedOk="}
	for _, token := range required {
		if !strings.Contains(stderrStr, token) {
			t.Errorf("audit line missing token %q in stderr: %q", token, stderrStr)
		}
	}
}
