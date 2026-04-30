package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHumanizeBytes covers the GO-3 boundary table: <4KiB stays raw bytes;
// 4KiB..1MiB renders as KiB with one decimal; >=1MiB renders as MiB with two.
func TestHumanizeBytes(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0B"},
		{47, "47B"},
		{4095, "4095B"},
		{4096, "4.0KiB"},
		{10240, "10.0KiB"},
		{1048575, "1024.0KiB"},
		{1048576, "1.00MiB"},
		{5 * 1024 * 1024, "5.00MiB"},
	}
	for _, c := range cases {
		got := humanizeBytes(c.n)
		if got != c.want {
			t.Errorf("humanizeBytes(%d) = %q; want %q", c.n, got, c.want)
		}
	}
}

// TestSave_GetStepWritesPayloadToFileWithConfirmation verifies that
// `workflow get-step --save <path>` routes the JSON payload to disk and
// emits a single "wrote NB to <abs>" line on stdout instead of dumping
// the full payload.
func TestSave_GetStepWritesPayloadToFileWithConfirmation(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	dir := t.TempDir()
	savePath := filepath.Join(dir, "step.json")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
		"--save", savePath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("get-step --save should succeed, got: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	if strings.Contains(out, `"stepId"`) {
		t.Errorf("--save should NOT dump payload to stdout, got: %q", out)
	}
	if !strings.HasPrefix(out, "wrote ") || !strings.Contains(out, savePath) {
		t.Errorf("expected confirmation 'wrote NB to %s', got stdout: %q", savePath, out)
	}

	saved, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved payload: %v", err)
	}
	var step map[string]any
	if err := json.Unmarshal(saved, &step); err != nil {
		t.Fatalf("saved payload not JSON: %v\ncontent: %s", err, saved)
	}
	if step["stepId"] != "STEP_01_BRAINSTORMING" {
		t.Errorf("saved payload missing stepId, got: %v", step)
	}
}

// TestSave_GetStepWithQuietSilencesConfirmation verifies that combining
// --save with --quiet leaves stdout completely empty on success.
func TestSave_GetStepWithQuietSilencesConfirmation(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	dir := t.TempDir()
	savePath := filepath.Join(dir, "step-quiet.json")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
		"--save", savePath,
		"--quiet",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("get-step --save --quiet should succeed, got: %v", err)
	}

	if stdout.Len() != 0 {
		t.Errorf("--save --quiet must zero stdout, got: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("--save --quiet must zero stderr on success, got: %q", stderr.String())
	}

	if _, err := os.Stat(savePath); err != nil {
		t.Errorf("payload file should exist: %v", err)
	}
}

// TestSave_GetStepWithFieldSavesNarrowedPayload verifies that --save
// composes correctly with --field — the saved file holds only the field
// extraction, not the whole step.
func TestSave_GetStepWithFieldSavesNarrowedPayload(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	dir := t.TempDir()
	savePath := filepath.Join(dir, "field.json")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
		"--field", "stepId",
		"--save", savePath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("get-step --field --save should succeed, got: %v\nstderr: %s", err, stderr.String())
	}

	saved, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved payload: %v", err)
	}
	if !strings.Contains(string(saved), "STEP_01_BRAINSTORMING") {
		t.Errorf("saved field payload missing value, got: %s", saved)
	}
	// Narrow payload — must NOT contain the full step structure.
	if strings.Contains(string(saved), "applicability") {
		t.Errorf("--field saved more than the field, got: %s", saved)
	}
}

// TestSave_GetStepDefaultStdoutPreservedWhenNoSave verifies the historic
// behaviour: without --save the payload still flows to stdout (no
// regression of read contracts).
func TestSave_GetStepDefaultStdoutPreservedWhenNoSave(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("get-step (no --save) should succeed, got: %v", err)
	}
	if !strings.Contains(stdout.String(), `"stepId"`) {
		t.Errorf("default mode must dump payload to stdout, got: %q", stdout.String())
	}
}

// TestSave_GetConfigFieldRoutesToFile verifies that get-config also honors
// --save: the value lands in the file, stdout gets the confirmation.
func TestSave_GetConfigFieldRoutesToFile(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	dir := t.TempDir()
	savePath := filepath.Join(dir, "config.txt")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-config", "mode",
		"--workflow", wfPath,
		"--save", savePath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("get-config --save should succeed, got: %v\nstderr: %s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "autonomous") {
		t.Errorf("--save should NOT dump value to stdout, got: %q", stdout.String())
	}
	if !strings.HasPrefix(stdout.String(), "wrote ") {
		t.Errorf("expected confirmation line, got: %q", stdout.String())
	}
	saved, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(saved), "autonomous") {
		t.Errorf("saved value missing, got: %q", saved)
	}
}

// TestSave_GetStepCreatesParentDir (QA-5) — --save to a path under a non-
// existent directory must create intermediate dirs via os.MkdirAll(0o755).
func TestSave_GetStepCreatesParentDir(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	dir := t.TempDir()
	// Three levels of non-existent nesting:
	savePath := filepath.Join(dir, "a", "b", "c", "step.json")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
		"--save", savePath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("--save into nested dir should succeed, got: %v\nstderr: %s", err, stderr.String())
	}

	if _, err := os.Stat(filepath.Dir(savePath)); err != nil {
		t.Errorf("expected parent dir to be created, got: %v", err)
	}
	saved, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.Contains(string(saved), `"stepId"`) {
		t.Errorf("expected payload at savePath, got: %s", saved)
	}
}

// TestSave_GetStepReadOnlyFsExitsNonZero (QA-6) — when the --save target's
// parent dir is unwritable, the helper must surface a wrapped '--save:'
// error and exit non-zero. Skipped on Windows (chmod semantics differ) and
// when running as root (chmod 0o555 is bypassed).
func TestSave_GetStepReadOnlyFsExitsNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0o555 read-only semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses 0o555")
	}
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	roDir := t.TempDir()
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatalf("chmod 0o555: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })
	savePath := filepath.Join(roDir, "denied.json")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
		"--save", savePath,
	})

	err := root.Execute()
	if err == nil {
		t.Error("--save into read-only fs should error")
	}
	combined := stderr.String() + (func() string {
		if err != nil {
			return err.Error()
		}
		return ""
	}())
	if !strings.Contains(combined, "--save") {
		t.Errorf("expected wrapped '--save' error, got: %q", combined)
	}
}

// TestSave_GetStepRenderToFile (QA-7) — --save composes with --render: the
// rendered text block (NOT JSON) lands in the file verbatim, stdout shows
// only the 'wrote NB to' confirmation. Closes both QA-7 (test gap) and the
// SR-1 cross-confirmation that --render output stays byte-identical to its
// pre-1.6.0 form (no auto-appended newline).
func TestSave_GetStepRenderToFile(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	dir := t.TempDir()
	savePath := filepath.Join(dir, "rendered.txt")

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
		"--render", "brainstorming",
		"--save", savePath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("--render --save should succeed, got: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	if !strings.HasPrefix(out, "wrote ") || !strings.Contains(out, savePath) {
		t.Errorf("expected confirmation 'wrote NB to %s', got stdout: %q", savePath, out)
	}

	saved, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved render: %v", err)
	}
	if len(saved) == 0 {
		t.Fatal("render output is empty")
	}
	// SR-1 byte-equality guard: emitReadRaw must NOT append a newline.
	// The render template either emits its own trailing newline or not — we
	// just assert that the saved bytes equal what cmd.OutOrStdout() would
	// have received without --save (i.e. no extra '\n' appended by the
	// helper). We can't easily roundtrip via two CLI runs in the same test,
	// so we assert the lighter invariant: if the rendered template emits a
	// trailing newline, the saved bytes have exactly ONE trailing newline
	// (not two — which would catch the regression where emitReadJSON's
	// newline-append leaks into the render path).
	doubleNewlineSuffix := strings.HasSuffix(string(saved), "\n\n")
	if doubleNewlineSuffix {
		t.Error("--render --save must not double-append newline (regression of SR-1)")
	}
}

// TestSave_GetStepOverwritesExistingFile (QA-8) — --save to a path that
// already contains different bytes must silently overwrite (os.WriteFile
// truncates). Pins the idempotent re-run contract for skill templates that
// may re-save $FEAT_DIR/.brainstorm.json on retry.
func TestSave_GetStepOverwritesExistingFile(t *testing.T) {
	wfPath := writeWorkflowFile(t, workflowWithStepsJSON)
	dir := t.TempDir()
	savePath := filepath.Join(dir, "overwrite.json")

	garbage := []byte(`{"old": "garbage that must be replaced"}`)
	if err := os.WriteFile(savePath, garbage, 0o600); err != nil {
		t.Fatalf("seed garbage: %v", err)
	}

	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommandT(t, &stdout, &stderr)
	root.SetArgs([]string{
		"workflow", "get-step", "STEP_01_BRAINSTORMING",
		"--workflow", wfPath,
		"--save", savePath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("overwrite --save should succeed, got: %v", err)
	}

	saved, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "garbage") {
		t.Errorf("--save must overwrite previous bytes, got: %s", saved)
	}
	if !strings.Contains(string(saved), `"stepId"`) {
		t.Errorf("--save did not write expected payload, got: %s", saved)
	}
}
