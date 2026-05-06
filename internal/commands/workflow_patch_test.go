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

// --- Bulk-mode coverage -----------------------------------------------------
//
// These tests close the test gap shipped alongside `--bulk` in commit
// 174feda4 ("feat(cli/workflow): patch --dry-run + --bulk (A1.4 + A3.3)"):
// the implementation works (verified by hand against real workflow.json
// fixtures) but the CLI surface had zero regression coverage. The retro at
// docs/SESSION_REPORT_20260505_orchestrate_task_delivery.md §9.1.4 listed
// "workflow patch em batch" as a friction; the impl shipped, the tests did
// not. We pin the contract here so future drift trips CI instead of the next
// orchestrate-task-delivery operator.

// TestPatch_BulkAppliesAllExpressionsSequentially asserts that every entry
// in the --bulk JSON array is applied against the in-memory mutation result
// of the previous one, leaving every targeted field set on disk after a
// single lock window + single CUE validation.
func TestPatch_BulkAppliesAllExpressionsSequentially(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--bulk", `[
			".featureName = \"bulk-A\"",
			".featureName += \"+B\"",
			".featureName += \"+C\""
		]`,
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("patch --bulk should succeed: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.FeatureName != "bulk-A+B+C" {
		t.Errorf("expected sequential composition, got featureName=%q", doc.FeatureName)
	}
}

// TestPatch_BulkSchemaViolationLeavesFileUnchanged asserts the single CUE
// validation runs against the FINAL post-pipeline document, and a violation
// rolls everything back. The first expression is valid; the second drops a
// required field. The on-disk file must be byte-identical to before.
func TestPatch_BulkSchemaViolationLeavesFileUnchanged(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--bulk", `[
			".featureName = \"would-survive\"",
			".schemaVersion = 0"
		]`,
		"--workflow", wfPath,
	})
	if err := root.Execute(); err == nil {
		t.Error("expected non-zero exit when bulk pipeline ends in schema violation")
	}

	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("schema-violating bulk pipeline must leave file untouched\nbefore-len=%d after-len=%d",
			len(before), len(after))
	}
}

// TestPatch_BulkAndJqMutuallyExclusive asserts the operator can't supply both
// flags. Catches the "--jq fed by stale template + --bulk fed by skill" case
// where one would silently win and the other would be ignored.
func TestPatch_BulkAndJqMutuallyExclusive(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--jq", `.featureName = "via-jq"`,
		"--bulk", `[".featureName = \"via-bulk\""]`,
		"--workflow", wfPath,
	})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when --jq and --bulk both supplied")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' in error, got: %v", err)
	}
}

// TestPatch_BulkRejectsInvalidJSONArray asserts a non-JSON --bulk argument
// trips early — before any lock acquisition or daemon dispatch — with a
// targeted error referencing the flag.
func TestPatch_BulkRejectsInvalidJSONArray(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--bulk", `not-an-array`,
		"--workflow", wfPath,
	})
	err = root.Execute()
	if err == nil {
		t.Fatal("expected non-zero exit for malformed --bulk JSON")
	}
	if !strings.Contains(err.Error(), "--bulk") {
		t.Errorf("expected error to name --bulk, got: %v", err)
	}

	after, _ := os.ReadFile(wfPath)
	if string(before) != string(after) {
		t.Error("malformed --bulk must not mutate workflow.json")
	}
}

// TestPatch_BulkRejectsEmptyArray asserts that `--bulk '[]'` (after stripping
// empty strings) refuses the no-op rather than silently exiting 0 — operators
// composing the array from templates would otherwise miss a missing variable
// expansion that produced no expressions.
func TestPatch_BulkRejectsEmptyArray(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--bulk", `["", " ", ""]`,
		"--workflow", wfPath,
	})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-zero exit for --bulk array with no non-empty expressions")
	}
	if !strings.Contains(err.Error(), "no non-empty expressions") {
		t.Errorf("expected 'no non-empty expressions' in error, got: %v", err)
	}
}

// TestPatch_BulkSingleLockWindowEmitsOneAuditLine asserts the bulk pipeline
// writes ONE audit line on success — confirming the lock-acquisition is
// shared across all N expressions (advisory lock, single tmp+rename, single
// validate). Mirrors TestPatch_DefaultEmitsAuditLine for the bulk path.
func TestPatch_BulkSingleLockWindowEmitsOneAuditLine(t *testing.T) {
	t.Setenv("BROWZER_LLM", "")
	t.Setenv("BROWZER_WORKFLOW_QUIET", "")
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--bulk", `[
			".featureName = \"bulk-audit-A\"",
			".featureName = \"bulk-audit-B\"",
			".featureName = \"bulk-audit-C\""
		]`,
		"--workflow", wfPath,
		"--sync",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("bulk: %v\nstderr: %s", err, stderr.String())
	}

	count := strings.Count(stderr.String(), "verb=patch")
	if count != 1 {
		t.Errorf("expected exactly 1 audit line, got %d\nstderr: %s", count, stderr.String())
	}
}

// TestResolvePatchExpression_BulkComposition unit-tests the helper that
// collapses --bulk into a single jq pipeline. Pinned because the composition
// is the load-bearing piece — if it ever drifts (e.g. switches from `|` to
// `,` or stops parenthesising), the bulk pipeline silently changes semantics.
func TestResolvePatchExpression_BulkComposition(t *testing.T) {
	cases := []struct {
		name     string
		jqExpr   string
		bulk     string
		want     string
		wantErr  bool
		errMatch string
	}{
		{
			name:   "single-expr bulk wraps in parens",
			bulk:   `[".foo = 1"]`,
			want:   `(.foo = 1)`,
			wantErr: false,
		},
		{
			name:   "multiple exprs joined by pipe",
			bulk:   `[".foo = 1", ".bar = 2"]`,
			want:   `(.foo = 1) | (.bar = 2)`,
			wantErr: false,
		},
		{
			name:   "empty entries skipped, surviving entries composed",
			bulk:   `["", ".foo = 1", "  ", ".bar = 2"]`,
			want:   `(.foo = 1) | (.bar = 2)`,
			wantErr: false,
		},
		{
			name:   "jq expr passes through when bulk empty",
			jqExpr: `.foo = 1`,
			bulk:   ``,
			want:   `.foo = 1`,
			wantErr: false,
		},
		{
			name:     "all-empty array errors",
			bulk:     `["", " ", "\t"]`,
			wantErr:  true,
			errMatch: "no non-empty expressions",
		},
		{
			name:     "non-array JSON errors",
			bulk:     `{"not": "array"}`,
			wantErr:  true,
			errMatch: "invalid JSON array",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePatchExpression(tc.jqExpr, tc.bulk)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none (got=%q)", got)
				}
				if tc.errMatch != "" && !strings.Contains(err.Error(), tc.errMatch) {
					t.Errorf("error %q does not match %q", err, tc.errMatch)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("composition drift:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// ---- --bulk × --dry-run cross-interaction (RETRO §C5, 2026-05-05) ----
//
// `--bulk` and `--dry-run` shipped in the same commit (174feda4) but no
// existing test covers what happens when they're combined. Operators
// will compose them — "preview my batch" is the natural use case. The
// shipped behaviour is pipeline-level preview: bulk expressions are
// composed into one jq pipeline, the pipeline runs once in-memory, and
// the post-pipeline document is validated + emitted on stdout. Each
// individual expression does NOT produce its own preview row.
//
// Pinning the contract here means a future refactor that accidentally
// switches to per-expression dry-run (or rejects the combo entirely)
// fails loudly instead of silently changing operator-visible
// behaviour.

func TestPatch_BulkAndDryRun_PreviewsAtPipelineLevel(t *testing.T) {
	// Use the v2-compliant fixture: ApplyDryRun does NOT honour the
	// BROWZER_NO_SCHEMA_CHECK env shortcut that the real apply path
	// uses, so the fixture must satisfy the live CUE schema directly.
	// (This asymmetry is documented behaviour in apply.go — dry-run is
	// validation-mandatory by design; the bypass is for write-only paths.)
	wfPath := writeWorkflowFile(t, validSchemaV2WorkflowJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "patch",
		"--bulk", `[
			".featureName = \"dry-A\"",
			".featureName += \"+B\"",
			".featureName += \"+C\""
		]`,
		"--dry-run",
		"--workflow", wfPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("patch --bulk --dry-run should succeed: %v\nstderr: %s\nstdout: %s",
			err, stderr.String(), stdout.String())
	}

	// File is unchanged (dry-run never persists).
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("--dry-run must leave workflow.json byte-identical")
	}

	// Stdout is a single JSON object (pipeline-level preview), NOT N
	// JSON lines (per-expression preview). The DryRunResult shape
	// (apply.go:DryRunResult) reports Ok + diffPaths + before/after
	// hashes — NOT the full post-mutation document. Pipeline-level
	// composition is confirmed by the diffPaths cardinality: a 3-step
	// chain that all touches `featureName` must produce ONE diff
	// entry (the pipeline's net change), not three separate ones.
	out := strings.TrimSpace(stdout.String())
	if strings.Count(out, "\n") > 0 {
		t.Errorf("expected single-line pipeline preview; got %d lines:\n%s",
			strings.Count(out, "\n")+1, out)
	}
	var preview struct {
		Ok         bool     `json:"ok"`
		Errors     []string `json:"errors"`
		BeforeHash string   `json:"beforeHash"`
		AfterHash  string   `json:"afterHash"`
		DiffPaths  []string `json:"diffPaths"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatalf("preview is not a single JSON object: %v\nraw: %s", err, out)
	}
	if !preview.Ok {
		t.Errorf("expected ok=true; errors=%v", preview.Errors)
	}
	if preview.BeforeHash == preview.AfterHash {
		t.Error("composed pipeline should change beforeHash → afterHash")
	}
	// Pipeline-level: ONE field changed. Per-expression dry-run would
	// emit N entries (one per step). The current contract is composed.
	featureNameTouched := false
	for _, p := range preview.DiffPaths {
		if p == "featureName" {
			featureNameTouched = true
		}
	}
	if !featureNameTouched {
		t.Errorf("expected diffPaths to include 'featureName' (pipeline-level diff); got %v",
			preview.DiffPaths)
	}
}

// TestPatch_BulkAndDryRun_ValidationFailureLeavesFileUnchanged stacks the
// existing schema-violation test with --dry-run: the file should remain
// unchanged AND the dry-run must surface the validation error in the JSON
// preview rather than silently passing.
func TestPatch_BulkAndDryRun_ValidationFailureLeavesFileUnchanged(t *testing.T) {
	wfPath := writeWorkflowFile(t, validSchemaV2WorkflowJSON)
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	// First expression is valid; second drops `featureId` which is
	// required by the v1 schema. The pipeline-level validation MUST
	// catch the violation.
	root.SetArgs([]string{
		"workflow", "patch",
		"--bulk", `[
			".featureName = \"dry-violate\"",
			"del(.featureId)"
		]`,
		"--dry-run",
		"--workflow", wfPath,
	})
	err = root.Execute()
	if err == nil {
		t.Fatal("expected non-zero exit for schema-violating dry-run")
	}

	after, _ := os.ReadFile(wfPath)
	if string(before) != string(after) {
		t.Error("dry-run with violation must not mutate workflow.json")
	}

	// Preview must include the failure on stdout.
	out := strings.TrimSpace(stdout.String())
	var preview struct {
		Ok     bool     `json:"ok"`
		Errors []string `json:"errors"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &preview); jsonErr != nil {
		t.Fatalf("preview not parseable as JSON: %v\nraw: %s", jsonErr, out)
	}
	if preview.Ok {
		t.Error("preview.ok must be false on validation failure")
	}
	if len(preview.Errors) == 0 {
		t.Error("preview.errors must list the validation failure(s)")
	}
}
