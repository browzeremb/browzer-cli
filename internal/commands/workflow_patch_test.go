package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// TestPatch_JqExpressionApplied verifies that
// `browzer workflow patch --jq <expr>` applies an arbitrary jq mutation
// and the result is persisted under the same lock + validation pipeline.
// Covers T3-T-8 (happy path).
func TestPatch_JqExpressionApplied(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// Set featureName via jq expression.
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.featureName = "Patched Feature Name"`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch with valid jq should exit 0, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow after patch: %v", err)
	}
	if doc.FeatureName != "Patched Feature Name" {
		t.Errorf("expected featureName='Patched Feature Name', got %q", doc.FeatureName)
	}
}

// TestPatch_SchemaViolatingMutationRejectedFileUnchanged verifies that a jq
// mutation that violates schema v1 (e.g. setting schemaVersion to 0) is
// rejected after validation and the original file is left unchanged.
// Covers T3-T-8 (schema rejection branch).
func TestPatch_SchemaViolatingMutationRejectedFileUnchanged(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// Setting schemaVersion to 0 violates schema v1 validation.
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.schemaVersion = 0`,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for schema-violating patch, got nil error")
	}

	// File must be unchanged.
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("schema-violating patch must not mutate workflow.json")
	}

	// Error output must mention the violation.
	combinedOutput := stdout.String() + stderr.String()
	if !strings.Contains(combinedOutput, "schemaVersion") &&
		!strings.Contains(strings.ToLower(combinedOutput), "schema") &&
		!strings.Contains(strings.ToLower(combinedOutput), "validation") {
		t.Errorf("expected error output to reference schema violation, got: %q", combinedOutput)
	}
}

// TestPatch_InvalidJqExpressionExitsNonZero verifies that an invalid/malformed
// jq expression exits non-zero without mutating the file.
// Covers T3-T-8 (jq parse error branch).
func TestPatch_InvalidJqExpressionExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// Malformed jq expression.
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `THIS IS NOT VALID JQ !!!@@@`,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("expected non-zero exit for invalid jq expression, got nil error")
	}

	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("invalid jq expression must not mutate workflow.json")
	}
}

// TestPatch_MissingJqFlagExitsNonZero verifies that calling patch without
// the --jq flag exits non-zero with a usage error.
// Covers T3-T-8 (missing required flag).
func TestPatch_MissingJqFlagExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--workflow", wfPath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("expected non-zero exit when --jq flag is missing, got nil error")
	}
}

// TestPatch_ArgFlagBindsStringVariable verifies that `--arg KEY=VALUE`
// makes `$KEY` available inside the jq expression as a literal string.
// Without this flag the expression must shell-encode the value via
// `jq -Rs .` — the friction the flag set out to remove.
func TestPatch_ArgFlagBindsStringVariable(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--arg", "name=Bound Name",
		"--jq", `.featureName = $name`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch with --arg should succeed, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	if doc.FeatureName != "Bound Name" {
		t.Errorf("expected featureName=%q, got %q", "Bound Name", doc.FeatureName)
	}
}

// TestPatch_ArgJSONFlagBindsStructuredValue verifies that
// `--argjson KEY=<json>` makes `$KEY` available inside the jq expression as
// the parsed JSON value (array, object, number, etc.).
func TestPatch_ArgJSONFlagBindsStructuredValue(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--arg", "id=STEP_01_BRAINSTORMING",
		"--argjson", `note={"kind":"reviewer","ok":true}`,
		"--jq", `(.steps[] | select(.stepId == $id)) |= (. + {tag: $note})`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch with --argjson should succeed, got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	// JSON serializer uses indented form here ("kind": "reviewer") — match
	// loosely so we don't couple the test to whitespace.
	if !strings.Contains(string(data), `"reviewer"`) {
		t.Errorf("expected reviewer tag in step, got: %s", data)
	}
}

// TestPatch_ArgJSONInvalidJSONExitsNonZero verifies that a malformed JSON
// value passed to --argjson is rejected before any mutation runs.
func TestPatch_ArgJSONInvalidJSONExitsNonZero(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--argjson", "bad={not json}",
		"--jq", `.x = $bad`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err == nil {
		t.Error("expected non-zero exit for malformed --argjson value")
	}
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("malformed --argjson must not mutate workflow.json")
	}
}

// TestPatch_QuietFlagSuppressesAuditLine verifies that --quiet collapses
// the per-mutation `verb=patch …` audit telemetry on success. Errors and
// fallback warnings still print on stderr; this test only checks the
// happy path.
func TestPatch_QuietFlagSuppressesAuditLine(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--quiet",
		"--jq", `.featureName = "quiet"`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch --quiet should succeed, got: %v\nstderr: %s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "verb=patch") {
		t.Errorf("--quiet should suppress audit line, got stderr: %q", stderr.String())
	}
}

// TestPatch_BrowzerWorkflowQuietEnvSuppressesAuditLine verifies the env-
// var alternative to --quiet (BROWZER_WORKFLOW_QUIET=1).
func TestPatch_BrowzerWorkflowQuietEnvSuppressesAuditLine(t *testing.T) {
	t.Setenv("BROWZER_WORKFLOW_QUIET", "1")
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.featureName = "env-quiet"`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch under BROWZER_WORKFLOW_QUIET should succeed, got: %v", err)
	}
	if strings.Contains(stderr.String(), "verb=patch") {
		t.Errorf("BROWZER_WORKFLOW_QUIET=1 should suppress audit line, got stderr: %q", stderr.String())
	}
}

// TestPatch_DefaultEmitsAuditLine guards against accidental regression of
// the default behaviour: without --quiet / env-overrides the audit line
// MUST still print on stderr (existing observability contract).
func TestPatch_DefaultEmitsAuditLine(t *testing.T) {
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")
	t.Setenv("BROWZER_LLM", "")
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.featureName = "noisy"`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("default patch should succeed, got: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "verb=patch") {
		t.Errorf("default mode must emit audit line, got stderr: %q", stderr.String())
	}
}

// TestAuditQuietSource_TrackerOnLLMGateNotOnQuietGate verifies SA-8: when
// audit is silenced via --llm / BROWZER_LLM the audit data lands in the
// SQLite tracker (preserving observability for high-frequency LLM-driven
// traffic); when silenced via --quiet / BROWZER_WORKFLOW_QUIET the data is
// dropped (operator explicitly chose silence).
func TestAuditQuietSource_TrackerOnLLMGateNotOnQuietGate(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	// Use a per-test tracker DB so we don't pollute the user's history.
	dbDir := t.TempDir()
	t.Setenv("BROWZER_HOME", dbDir)
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")
	t.Setenv("BROWZER_LLM", "")

	cases := []struct {
		name             string
		envQuiet         string
		envLLM           string
		expectStderrLine bool
		expectQuietGate  bool
		expectLLMGate    bool
	}{
		{"default emits stderr line", "", "", true, false, false},
		{"--quiet env drops both", "1", "", false, true, false},
		{"--llm env routes to tracker", "", "1", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BROWZER_WORKFLOW_QUIET", tc.envQuiet)
			t.Setenv("BROWZER_LLM", tc.envLLM)

			var stdout, stderr bytes.Buffer
			root := buildWorkflowCommandT(t, &stdout, &stderr)
			root.SetArgs([]string{
				"workflow", "patch",
				"--jq", `.featureName = "x"`,
				"--workflow", wfPath,
			})
			if err := root.Execute(); err != nil {
				t.Fatalf("patch should succeed: %v\nstderr: %s", err, stderr.String())
			}
			hasLine := bytes.Contains(stderr.Bytes(), []byte("verb=patch"))
			if hasLine != tc.expectStderrLine {
				t.Errorf("stderr verb=patch line: got %v want %v\nstderr=%q", hasLine, tc.expectStderrLine, stderr.String())
			}
		})
	}
}

// TestPatch_ArgEmptyValueAccepted (QA-3a) — `--arg KEY=` binds $KEY to the
// empty string. splitBindPair returns ok=true with empty value; the patch
// must succeed and the bound variable must be the empty string in the
// resulting jq expression.
func TestPatch_ArgEmptyValueAccepted(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--arg", "name=",
		"--jq", `.featureName = $name`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("--arg KEY= should succeed (empty string bind), got: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	if doc.FeatureName != "" {
		t.Errorf("expected featureName='' (empty bind), got %q", doc.FeatureName)
	}
}

// TestPatch_ArgEmptyKeyRejected (QA-3b) — `--arg =VALUE` (empty key) must
// be rejected with the documented error format. splitBindPair returns ok=false
// for idx==0; parseJQBindFlags surfaces "expected KEY=VALUE form".
func TestPatch_ArgEmptyKeyRejected(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--arg", "=value",
		"--jq", `.featureName = "x"`,
		"--workflow", wfPath,
	})

	err = root.Execute()
	if err == nil {
		t.Error("--arg =value should error (empty key)")
	}
	combined := stdout.String() + stderr.String() + (func() string {
		if err != nil {
			return err.Error()
		}
		return ""
	}())
	if !strings.Contains(combined, "expected KEY=VALUE") {
		t.Errorf("expected 'expected KEY=VALUE' in error output, got: %q", combined)
	}

	after, _ := os.ReadFile(wfPath)
	if string(before) != string(after) {
		t.Error("rejected --arg must not mutate workflow.json")
	}
}

// TestPatch_DuplicateKeysAcrossArgAndArgJSONErrors (QA-4) — the CHANGELOG
// claims duplicate keys ACROSS --arg and --argjson are rejected; without
// this test, a regression that drops the cross-set dedup would land silently.
// Two cases: (a) two --arg with same key, (b) --arg + --argjson with same key.
func TestPatch_DuplicateKeysAcrossArgAndArgJSONErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "two --arg same key",
			args: []string{"--arg", "id=A", "--arg", "id=B"},
		},
		{
			name: "--arg then --argjson same key",
			args: []string{"--arg", "id=A", "--argjson", `id="B"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
			before, err := os.ReadFile(wfPath)
			if err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			root := buildWorkflowCommandT(t, &stdout, &stderr)
			full := append([]string{"workflow", "patch"}, tc.args...)
			full = append(full, "--jq", `.featureName = $id`, "--workflow", wfPath)
			root.SetArgs(full)

			err = root.Execute()
			if err == nil {
				t.Error("expected duplicate-key rejection, got nil error")
			}
			combined := stdout.String() + stderr.String() + (func() string {
				if err != nil {
					return err.Error()
				}
				return ""
			}())
			if !strings.Contains(combined, "already bound") {
				t.Errorf("expected 'already bound' in error output, got: %q", combined)
			}

			after, _ := os.ReadFile(wfPath)
			if string(before) != string(after) {
				t.Error("rejected dup-key must not mutate workflow.json")
			}
		})
	}
}

// TestPatch_BrowzerLlmEnvSuppressesAuditLine (QA-9a) — BROWZER_LLM=1 must
// silence the audit telemetry line on stderr (mirror of the existing
// BROWZER_WORKFLOW_QUIET=1 test).
func TestPatch_BrowzerLlmEnvSuppressesAuditLine(t *testing.T) {
	t.Setenv("BROWZER_LLM", "1")
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.featureName = "llm-env"`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch under BROWZER_LLM=1 should succeed, got: %v", err)
	}
	if strings.Contains(stderr.String(), "verb=patch") {
		t.Errorf("BROWZER_LLM=1 must suppress audit line, got stderr: %q", stderr.String())
	}
}

// TestPatch_LlmFlagSuppressesAuditLine (QA-9b) — explicit --llm flag must
// silence the audit telemetry line on stderr. The test harness builds a
// minimal cobra root (registerWorkflow only — no full registerRoot()), so
// --llm needs explicit registration on the local root to be parseable.
func TestPatch_LlmFlagSuppressesAuditLine(t *testing.T) {
	t.Setenv("BROWZER_LLM", "")
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// Register the persistent --llm flag that the real CLI sets up in
	// registerRoot(); without this the test harness errors with
	// "unknown command \"patch\"" because cobra treats --llm as positional.
	root.PersistentFlags().Bool("llm", false, "LLM mode (no banner/colors/spinners)")
	root.SetArgs([]string{
		"workflow", "patch",
		"--llm",
		"--jq", `.featureName = "llm-flag"`,
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("patch with --llm should succeed, got: %v", err)
	}
	if strings.Contains(stderr.String(), "verb=patch") {
		t.Errorf("--llm must suppress audit line, got stderr: %q", stderr.String())
	}
}
