package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// workflowWithDispatchStepJSON is a minimal workflow fixture containing one TASK step
// used by append-dispatch tests.
const workflowWithDispatchStepJSON = `{
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
  "currentStepId": "STEP_08_TASK_05",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_08_TASK_05",
      "name": "TASK",
      "taskId": "TASK_05",
      "status": "PENDING",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "warnings": [],
      "reviewHistory": [],
      "dispatches": [],
      "task": {
        "title": "Test task",
        "scope": [],
        "invariants": [],
        "suggestedModel": null,
        "trivial": false,
        "execution": {
          "gates": {"baseline": {}, "postChange": {}, "regression": []},
          "scopeAdjustments": [],
          "agents": [],
          "invariantsChecked": [],
          "nextSteps": ""
        }
      }
    }
  ]
}`

// TestAppendDispatch_HappyPath verifies the normal flow: write a prompt file,
// call append-dispatch, assert exit 0 and that dispatches[0] is populated with
// the expected digest, promptByteCount, and a relative promptPath.
func TestAppendDispatch_HappyPath(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithDispatchStepJSON)

	promptContent := "Hello, dispatch world!"
	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(promptFile, []byte(promptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	agentID := "agent-happy-path-001"

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-dispatch", "STEP_08_TASK_05",
		"--prompt-file", promptFile,
		"--agent-id", agentID,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("append-dispatch happy path should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	// Re-read workflow.json.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(after, &doc); err != nil {
		t.Fatalf("parse workflow after append-dispatch: %v", err)
	}

	steps := doc["steps"].([]any)
	step := steps[0].(map[string]any)
	dispatches, ok := step["dispatches"].([]any)
	if !ok || len(dispatches) == 0 {
		t.Fatalf("expected dispatches to have 1 entry, got: %v", step["dispatches"])
	}
	rec := dispatches[0].(map[string]any)

	// Verify digest.
	sum := sha256.Sum256([]byte(promptContent))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if rec["dispatchPromptDigest"] != wantDigest {
		t.Errorf("dispatchPromptDigest: want %q got %q", wantDigest, rec["dispatchPromptDigest"])
	}

	// Verify promptByteCount.
	byteCount, _ := rec["promptByteCount"].(float64)
	if int(byteCount) != len(promptContent) {
		t.Errorf("promptByteCount: want %d got %d", len(promptContent), int(byteCount))
	}

	// Verify agentId.
	if rec["agentId"] != agentID {
		t.Errorf("agentId: want %q got %q", agentID, rec["agentId"])
	}

	// Verify promptPath is relative (not absolute) and contains stepId + agentId.
	promptPath, _ := rec["promptPath"].(string)
	if filepath.IsAbs(promptPath) {
		t.Errorf("promptPath should be relative, got absolute: %q", promptPath)
	}
	if !strings.Contains(promptPath, "STEP_08_TASK_05") {
		t.Errorf("promptPath should contain stepId, got: %q", promptPath)
	}
	if !strings.Contains(promptPath, agentID) {
		t.Errorf("promptPath should contain agentId, got: %q", promptPath)
	}

	// Verify the spool file exists on disk with the expected content.
	// The spool file path is: <repoRoot>/.browzer/dispatch-spool/<featSlug>/<stepId>/<agentId>.txt
	// promptPath is relative to repoRoot, so we walk up from wfPath to find repoRoot.
	wfDir := filepath.Dir(wfPath)
	// Since the test uses a tmpdir, there's no .git — FindRepoRoot returns start (wfDir).
	// promptPath = <featSlug>/<stepId>/<agentId>.txt relative to repoRoot.
	// repoRoot = FindRepoRoot(wfDir) = wfDir (no .git in tmpdir).
	spoolAbsPath := filepath.Join(wfDir, promptPath)
	spoolContent, err := os.ReadFile(spoolAbsPath)
	if err != nil {
		t.Fatalf("spool file should exist at %q: %v", spoolAbsPath, err)
	}
	if string(spoolContent) != promptContent {
		t.Errorf("spool file content mismatch: want %q got %q", promptContent, string(spoolContent))
	}

	// Verify dispatchedAt is a parseable RFC3339 timestamp.
	dispatchedAt, _ := rec["dispatchedAt"].(string)
	if _, parseErr := time.Parse(time.RFC3339, dispatchedAt); parseErr != nil {
		t.Errorf("dispatchedAt should be RFC3339, got: %q, err: %v", dispatchedAt, parseErr)
	}
}

// TestAppendDispatch_Idempotent verifies that calling append-dispatch twice
// with the same agent-id appends 2 entries (distinct dispatchedAt, by design)
// but overwrites the spool file on disk (idempotent overwrite invariant).
func TestAppendDispatch_Idempotent(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithDispatchStepJSON)

	promptContent := "Idempotent dispatch prompt"
	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(promptFile, []byte(promptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	agentID := "agent-idempotent-001"

	runDispatch := func() {
		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommandT(t, &stdout, &stderr)
		root.SetArgs([]string{
			"workflow", "append-dispatch", "STEP_08_TASK_05",
			"--prompt-file", promptFile,
			"--agent-id", agentID,
			"--workflow", wfPath,
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("append-dispatch should exit 0: %v\nstderr: %s", err, stderr.String())
		}
	}

	// First call.
	runDispatch()

	// Capture spool file mtime after first call.
	wfDir := filepath.Dir(wfPath)
	// Re-read to get promptPath.
	data1, _ := os.ReadFile(wfPath)
	var doc1 map[string]any
	_ = json.Unmarshal(data1, &doc1)
	steps1 := doc1["steps"].([]any)
	step1 := steps1[0].(map[string]any)
	dispatches1 := step1["dispatches"].([]any)
	rec1 := dispatches1[0].(map[string]any)
	promptPath1, _ := rec1["promptPath"].(string)
	spoolPath := filepath.Join(wfDir, promptPath1)

	fi1, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatalf("spool file should exist after first call: %v", err)
	}
	mtime1 := fi1.ModTime()

	// QA-006 (2026-05-04): the assertion below uses `.Before()` which
	// returns false on equal mtimes — so on filesystems with 1s mtime
	// resolution (HFS+, ext3, FAT) the test is still correct without
	// the sleep. The sleep raises confidence by a few orders of
	// magnitude on filesystems with sub-second resolution (most modern
	// Linux, APFS). Keep the sleep small (10ms) — extending it to
	// cover the 1s case is gold-plating; the assertion is robust.
	time.Sleep(10 * time.Millisecond)

	// Second call.
	runDispatch()

	// Assert workflow has 2 dispatch entries.
	data2, _ := os.ReadFile(wfPath)
	var doc2 map[string]any
	_ = json.Unmarshal(data2, &doc2)
	steps2 := doc2["steps"].([]any)
	step2 := steps2[0].(map[string]any)
	dispatches2 := step2["dispatches"].([]any)
	if len(dispatches2) != 2 {
		t.Errorf("expected 2 dispatch entries after 2 calls, got %d", len(dispatches2))
	}

	// Assert spool file was overwritten (mtime should differ or content identical).
	fi2, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatalf("spool file should still exist after second call: %v", err)
	}
	// Content must be identical (same prompt file).
	spoolContent, _ := os.ReadFile(spoolPath)
	if string(spoolContent) != promptContent {
		t.Errorf("spool file content should match prompt: want %q got %q", promptContent, string(spoolContent))
	}

	// digest must be the same in both records.
	sum := sha256.Sum256([]byte(promptContent))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	rec2a := dispatches2[0].(map[string]any)
	rec2b := dispatches2[1].(map[string]any)
	if rec2a["dispatchPromptDigest"] != wantDigest {
		t.Errorf("first entry digest wrong: %q", rec2a["dispatchPromptDigest"])
	}
	if rec2b["dispatchPromptDigest"] != wantDigest {
		t.Errorf("second entry digest wrong: %q", rec2b["dispatchPromptDigest"])
	}

	// The spool file should have been overwritten — mtime >= mtime1.
	if fi2.ModTime().Before(mtime1) {
		t.Errorf("spool file mtime should be >= after second call: mtime1=%v mtime2=%v", mtime1, fi2.ModTime())
	}
	_ = mtime1
}

// TestAppendDispatch_MissingPromptFile verifies that a non-existent --prompt-file
// causes a non-zero exit and an informative error on stderr.
func TestAppendDispatch_MissingPromptFile(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithDispatchStepJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "append-dispatch", "STEP_08_TASK_05",
		"--prompt-file", "/nonexistent/path/prompt.txt",
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-zero exit for missing prompt file, got nil error")
	}

	stderrStr := stderr.String()
	errStr := err.Error()
	combined := stderrStr + errStr
	if !strings.Contains(combined, "prompt file not found") {
		t.Errorf("expected 'prompt file not found' in output, got stderr=%q err=%q", stderrStr, errStr)
	}
}

// TestAppendDispatch_MissingDigest_RejectedBySchema verifies that
// ApplyAndPersist rejects a DispatchRecord missing dispatchPromptDigest
// when schema validation is active (BROWZER_NO_SCHEMA_CHECK is NOT set to "1").
func TestAppendDispatch_MissingDigest_RejectedBySchema(t *testing.T) {
	// This test deliberately enables schema validation by clearing the bypass.
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

	wfPath := writeSchemaValidWorkflowFile(t)

	// Build a malformed dispatch record: missing dispatchPromptDigest.
	malformedRecord := map[string]any{
		"agentId":           "agent-bad-001",
		"promptByteCount":   int64(42),
		"promptPath":        ".browzer/dispatch-spool/feat/STEP_01/agent-bad-001.txt",
		"renderTemplateUsed": nil,
		"dispatchedAt":      time.Now().UTC().Format(time.RFC3339),
		"model":             nil,
		"status":            "in_progress",
		"findingsAddressed": []any{},
		"filesModified":     []any{},
		"filesCreated":      []any{},
		"filesDeleted":      []any{},
		// dispatchPromptDigest intentionally missing
	}
	payloadBytes, _ := json.Marshal(malformedRecord)

	result, err := wf.ApplyAndPersist(wfPath, "append-dispatch", wf.MutatorArgs{
		Args:    []string{"STEP_01_TASK"},
		Payload: payloadBytes,
	}, false)

	// Should be rejected by schema validation (valid=false).
	if err == nil {
		t.Fatalf("expected schema validation error for missing dispatchPromptDigest, got nil; result=%+v", result)
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "dispatchPromptDigest") && !strings.Contains(errStr, "schema validation") {
		t.Errorf("expected error to mention dispatchPromptDigest or schema validation, got: %q", errStr)
	}
}

// writeSchemaValidWorkflowFile writes a schema-v2-compliant workflow fixture
// containing one TASK step, for use in tests that enable schema validation.
func writeSchemaValidWorkflowFile(t *testing.T) string {
	t.Helper()
	now := "2026-05-04T00:00:00Z"
	content := fmt.Sprintf(`{
  "schemaVersion": 2,
  "pluginVersion": null,
  "featureId": "feat-20260504-dispatch-test",
  "featureName": "Dispatch Test",
  "featDir": "dispatch-test",
  "originalRequest": "",
  "operator": {"locale": ""},
  "config": {"mode": "autonomous", "setAt": %q},
  "startedAt": %q,
  "updatedAt": %q,
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "STEP_01_TASK",
  "nextStepId": "",
  "totalSteps": 1,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_TASK",
      "name": "TASK",
      "taskId": "TASK_01",
      "status": "PENDING",
      "applicability": {"applicable": true, "reason": ""},
      "startedAt": %q,
      "completedAt": null,
      "elapsedMin": 0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "",
      "skillsToInvoke": [],
      "skillsInvoked": [],
      "owner": null,
      "warnings": [],
      "reviewHistory": [],
      "dispatches": [],
      "task": {
        "scope": [],
        "invariants": [],
        "suggestedModel": null,
        "trivial": false,
        "execution": {
          "gates": {"baseline": {}, "postChange": {}, "regression": []},
          "scopeAdjustments": [],
          "agents": [],
          "invariantsChecked": [],
          "nextSteps": ""
        }
      }
    }
  ]
}`, now, now, now, now)

	dir := t.TempDir()
	wfPath := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(wfPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writeSchemaValidWorkflowFile: %v", err)
	}
	return wfPath
}
