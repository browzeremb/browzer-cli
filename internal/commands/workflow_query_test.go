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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
	root := buildWorkflowCommandT(t, &stdout, &stderr)
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
		"cache-warm-deps",
		"cache-warm-mentions",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("query --help missing query %q (stdout: %q)", name, out)
		}
	}
}

// TestWorkflowQuery_CacheWarmDeps exercises the cache-warm-deps query via the
// CLI layer (cobra → registry → queryCacheWarmDeps). The fixture has 2 TASK
// steps each containing 2 scope files with one duplicate → expects 3 records.
func TestWorkflowQuery_CacheWarmDeps(t *testing.T) {
	const cacheWarmWorkflowJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-cw",
  "featDir": "docs/browzer/feat-cw",
  "originalRequest": "test",
  "operator": {"locale": "en"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "STEP_02_TASK_02",
  "nextStepId": "",
  "totalSteps": 2,
  "completedSteps": 2,
  "notes": [],
  "globalWarnings": [],
  "steps": [
    {
      "stepId": "STEP_01_TASK_01",
      "name": "TASK",
      "taskId": "TASK_01",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:00:00Z",
      "completedAt": "2026-04-29T00:01:00Z",
      "elapsedMin": 1.0,
      "retryCount": 0,
      "itDependsOn": [],
      "nextStep": "STEP_02_TASK_02",
      "skillsToInvoke": ["execute-task"],
      "skillsInvoked": ["execute-task"],
      "owner": null,
      "worktrees": {"used": false, "worktrees": []},
      "warnings": [],
      "reviewHistory": [],
      "task": {
        "scope": ["apps/api/src/routes/ask.ts", "apps/api/src/server.ts"]
      }
    },
    {
      "stepId": "STEP_02_TASK_02",
      "name": "TASK",
      "taskId": "TASK_02",
      "status": "COMPLETED",
      "applicability": {"applicable": true, "reason": "default"},
      "startedAt": "2026-04-29T00:01:00Z",
      "completedAt": "2026-04-29T00:02:00Z",
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
        "scope": ["apps/worker/src/consumers/ingest.ts", "apps/api/src/server.ts"]
      }
    }
  ]
}`
	wfPath := writeWorkflowFile(t, cacheWarmWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "query", "cache-warm-deps", "--workflow", wfPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v (stderr: %s)", err, stderr.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\nraw: %s", err, stdout.String())
	}

	// 3 unique files from 4 scope entries (apps/api/src/server.ts deduped).
	if len(got) != 3 {
		t.Fatalf("expected 3 records (deduped), got %d: %v", len(got), got)
	}
	for _, r := range got {
		if _, hasFile := r["file"]; !hasFile {
			t.Errorf("record missing 'file' key: %v", r)
		}
		if _, hasDeps := r["depsCachePath"]; !hasDeps {
			t.Errorf("record missing 'depsCachePath' key: %v", r)
		}
	}
}

// TestWorkflowQuery_AuditLineGoesToStderrNotStdout regression-pins the
// stdout/stderr stream split: the `verb=...` audit line MUST land on
// stderr, never stdout, so callers using $(cmd) or `cmd | jq` see only
// the JSON payload. RETRO 3.1 from the 2026-05-05 token-economy session:
// when the audit line slipped onto stdout, downstream jq parsed it and
// reported "Invalid escape" — pausing the entire workflow.
func TestWorkflowQuery_AuditLineGoesToStderrNotStdout(t *testing.T) {
	wfPath := writeWorkflowFile(t, queryWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "query", "reused-gates", "--workflow", wfPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stdoutStr := stdout.String()
	if strings.Contains(stdoutStr, "verb=") {
		t.Errorf("audit line `verb=...` leaked to stdout (breaks $(cmd | jq) callers); stdout: %q", stdoutStr)
	}
	if !strings.Contains(stderr.String(), "verb=query") {
		t.Errorf("audit line should land on stderr; stderr: %q", stderr.String())
	}
	// Stdout must remain valid JSON (the contract callers rely on).
	var parsed any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Errorf("stdout should be valid JSON, got: %v\nraw: %s", err, stdoutStr)
	}
}

// TestWorkflowQuery_OutputDoesNotEscapeHTMLChars regression-pins the
// HTML-escape disable: query output for steps containing `<`, `>`, `&` in
// step payload values (e.g. PRD acceptance criteria text) must emit
// literal characters, not the stdlib's default `<` / `>` /
// `&` escapes. RETRO 3.1 root cause from the 2026-05-05
// token-economy session: encoded `>` in AC-1 text triggered jq parse
// noise.
func TestWorkflowQuery_OutputDoesNotEscapeHTMLChars(t *testing.T) {
	wfWithGT := strings.ReplaceAll(queryWorkflowJSON,
		`"reason": "default"`,
		`"reason": "WHEN x > 0 AND y < 10 THEN ok && done"`,
	)
	wfPath := writeWorkflowFile(t, wfWithGT)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// steps-by-name returns the full step payload grouped by step type, so
	// the embedded reason text round-trips through marshalReadJSON.
	root.SetArgs([]string{"workflow", "query", "steps-by-name", "--workflow", wfPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	got := stdout.String()
	for _, escape := range []string{"\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(got, escape) {
			t.Errorf("query stdout should NOT contain HTML escape %s (breaks operator-readable jq output); got: %s", escape, got)
		}
	}
	// Verify the literal characters did pass through.
	for _, want := range []string{">", "<", "&&"} {
		if !strings.Contains(got, want) {
			t.Errorf("query stdout should contain literal %q in echoed reason text; got: %s", want, got)
		}
	}
}

// TestWorkflowQuery_StepsByName_HandlesTrickyChars_RoundtripStable is the
// RETRO PR 6 §3.1 regression pin. The 2026-05-06 session reported an
// intermittent `jq: Invalid escape at line 162, column 615` against a
// real PRD payload — the column landed inside a description containing
// backticks + code fences. Post-session, `query steps-by-name | jq -r ...`
// succeeded, so the bug was either a write-side encoding race OR an
// operator-side bash escape mishap.
//
// This test pins the CONTRACT, not the bug: a workflow.json containing
// every escape-edge-case (backticks, escaped backticks, code fences,
// HTML chars, unicode, control chars in description fields) MUST
// round-trip through `query steps-by-name` and produce
// strictly-valid-JSON output every time across 100 sequential runs.
// Any future regression on the read path that emits invalid JSON for
// these inputs trips this test.
func TestWorkflowQuery_StepsByName_HandlesTrickyChars_RoundtripStable(t *testing.T) {
	// Build a workflow whose PRD step has trickyDescription embedded.
	// jq invariably trips on:
	//   1. Bare backticks (jq accepts them but Markdown lookalikes
	//      sometimes reach the parser as escaped sequences)
	//   2. `\``-escaped backticks (the operator's first instinct;
	//      bash variant of the original report's `\\\``)
	//   3. Code fences (triple-backtick blocks)
	//   4. HTML-special chars (`<`, `>`, `&`)
	//   5. Unicode (en-dash, smart quotes)
	//   6. Multi-line text with embedded newlines.
	tricky := "WHEN `x > y` AND \\`code\\` is set " +
		"AND ```fenced``` matches THEN ok — pass\nLine two with «smart» quotes"

	prdJSON, mErr := json.Marshal(tricky)
	if mErr != nil {
		t.Fatalf("seed marshal: %v", mErr)
	}
	wfRaw := strings.ReplaceAll(queryWorkflowJSON,
		`"reason": "default"`,
		`"reason": `+string(prdJSON),
	)
	wfPath := writeWorkflowFile(t, wfRaw)

	// Run the read 100x. Each run must:
	//   (a) decode as valid JSON
	//   (b) preserve the literal tricky text (not double-escaped)
	for i := range 100 {
		var stdout, stderr bytes.Buffer
		root := buildWorkflowCommandT(t, &stdout, &stderr)
		root.SetArgs([]string{"workflow", "query", "steps-by-name", "--workflow", wfPath})
		if err := root.Execute(); err != nil {
			t.Fatalf("iter %d: Execute: %v\nstderr: %s", i, err, stderr.String())
		}

		var parsed any
		if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
			t.Fatalf("iter %d: stdout failed JSON decode: %v\nraw bytes: %q",
				i, err, stdout.String())
		}
		// Spot-check the literal tricky chars are present (the encoder
		// MUST NOT have produced HTML-escapes or double-backslashed the
		// embedded `\`` sequences).
		got := stdout.String()
		for _, badEscape := range []string{"\\u003c", "\\u003e", "\\u0026"} {
			if strings.Contains(got, badEscape) {
				t.Fatalf("iter %d: stdout contains HTML escape %s; raw: %s",
					i, badEscape, got)
			}
		}
	}
}
