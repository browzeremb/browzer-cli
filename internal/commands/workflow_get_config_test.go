package commands

import (
	"bytes"
	"strings"
	"testing"
)

// TestGetConfig_ModeReturnsUnquoted verifies that `browzer workflow get-config mode`
// returns "autonomous" or "review" as an unquoted string (raw value, not JSON).
// Covers T1-T-r4: get-config mode returns autonomous|review unquoted.
func TestGetConfig_ModeReturnsUnquoted(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON) // mode is "autonomous"

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out != "autonomous" {
		t.Errorf("expected %q, got %q", "autonomous", out)
	}
	// Must NOT be quoted as JSON string.
	if strings.HasPrefix(out, `"`) {
		t.Errorf("value should not be JSON-quoted, got %q", out)
	}
}

// TestGetConfig_ReviewModeReturnsUnquoted verifies the same for "review" mode.
// Covers T1-T-r4: get-config mode returns review unquoted.
func TestGetConfig_ReviewModeReturnsUnquoted(t *testing.T) {
	reviewContent := strings.ReplaceAll(minimalWorkflowJSON, `"mode": "autonomous"`, `"mode": "review"`)
	wfPath := writeWorkflowFile(t, reviewContent)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out != "review" {
		t.Errorf("expected %q, got %q", "review", out)
	}
}

// TestGetConfig_FieldNestedPath verifies that --field config-style nested
// paths (e.g. setAt) are supported.
// Covers T1-T-r4: --field config-style nested paths supported.
func TestGetConfig_FieldNestedPath(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "setAt", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Error("expected non-empty value for setAt, got empty")
	}
	// setAt is a timestamp string — should be unquoted.
	if strings.HasPrefix(out, `"`) {
		t.Errorf("setAt should be unquoted, got %q", out)
	}
}

// TestGetConfig_UnknownKeyExitsNonZero verifies that requesting an unknown
// config key exits non-zero.
// Covers T1-T-r4: error path for unknown config key.
func TestGetConfig_UnknownKeyExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "nonExistentKey", "--workflow", wfPath})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for unknown config key, got nil error")
	}
}

// TestGetConfig_StdoutDoesNotContainBannerVerb regression-pins the
// stdout/stderr split: `get-config` stdout must contain only the value
// payload, never the audit banner (`verb=...`). Mirrors the pin already
// in place for `query` (RETRO §C2 of 2026-05-05). Without this test, a
// future refactor of the audit-line emit path could re-introduce the
// banner-on-stdout bug that broke `$(cmd | jq)` pipelines.
func TestGetConfig_StdoutDoesNotContainBannerVerb(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)
	t.Setenv("BROWZER_LLM", "")
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode", "--workflow", wfPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "verb=") {
		t.Errorf("get-config stdout must not contain banner artifact `verb=`; got: %s", stdout.String())
	}
}

// TestGetConfig_TopLevelStateKey_CurrentStepId verifies the fall-through
// behavior added by FIX-1 (DOG-CONFIG-FALLTHROUGH, 2026-05-06): top-level
// state fields like `currentStepId`, `nextStepId`, `featureId`, … are
// readable via `get-config <key>` even though they live outside `.config`.
//
// Without this fall-through, the documented orchestrator pattern
//
//	LAST=$(browzer workflow get-config currentStepId --workflow "$WF")
//
// (cited in `packages/cli/CLAUDE.md` §".browzer/active-step hybrid-cache",
// `packages/skills/skills/orchestrate-task-delivery/SKILL.md` Step 4, and
// `packages/skills/skills/orchestrate-task-delivery/references/pipeline-phases.md`
// "Read verbs" table) emits "field not found" because `currentStepId` is at
// the workflow root, not under `.config`. The dogfood validation 2026-05-06
// caught the drift; this test pins the fix.
func TestGetConfig_TopLevelStateKey_CurrentStepId(t *testing.T) {
	// minimalWorkflowJSON sets currentStepId to "" — patch to a real id to
	// confirm we read the value, not the default.
	withId := strings.ReplaceAll(minimalWorkflowJSON, `"currentStepId": ""`, `"currentStepId": "STEP_02_PRD"`)
	wfPath := writeWorkflowFile(t, withId)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "currentStepId", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	got := strings.TrimSpace(stdout.String())
	if got != "STEP_02_PRD" {
		t.Errorf("expected currentStepId=%q, got %q", "STEP_02_PRD", got)
	}
	// Top-level state keys must NOT be JSON-quoted (matches `mode` semantics).
	if strings.HasPrefix(got, `"`) {
		t.Errorf("currentStepId should be unquoted, got %q", got)
	}
}

// TestGetConfig_TopLevelStateKey_AllScalarKeys exercises every key in
// `topLevelStateKeys` to guard against a regression that drops one entry
// from the allowlist. Each key must read without error and emit a non-empty
// value (or empty for keys whose fixture default is "").
func TestGetConfig_TopLevelStateKey_AllScalarKeys(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	keys := []string{
		"schemaVersion", "featureId", "featureName", "featDir", "originalRequest",
		"startedAt", "updatedAt", "totalElapsedMin", "currentStepId", "nextStepId",
		"totalSteps", "completedSteps",
	}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := buildWorkflowCommandT(t, &stdout, &stderr)
			root.SetArgs([]string{"workflow", "get-config", k, "--workflow", wfPath})
			if err := root.Execute(); err != nil {
				t.Fatalf("get-config %s: unexpected error %v\nstderr: %s", k, err, stderr.String())
			}
			// We don't assert specific values here — the contract under test
			// is "key resolves without 'field not found'". Empty stdout is
			// acceptable for keys whose fixture default is "".
		})
	}
}

// TestGetConfig_ConfigKeyStillResolves guards against a regression that
// would short-circuit on the top-level allowlist and miss the `.config.<key>`
// fall-through. `mode` lives under `.config` and must continue to resolve.
func TestGetConfig_ConfigKeyStillResolves(t *testing.T) {
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{"workflow", "get-config", "mode", "--workflow", wfPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "autonomous" {
		t.Errorf("expected mode=autonomous, got %q", got)
	}
}
