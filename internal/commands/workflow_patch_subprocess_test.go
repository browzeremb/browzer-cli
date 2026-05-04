package commands

// CMD-LINE-SURFACE subprocess tests for `browzer workflow patch --arg KEY=VALUE --jq '...'`.
//
// These tests invoke the actual CLI binary as a subprocess to exercise the
// real daemon-sync dispatch path — including the daemon handshake, JQVars
// routing fix (WF-SYNC-1), and audit-line emission. They are intentionally
// slow (binary build + daemon lifecycle); skip with -short.
//
// Test names match the testSpec T-1 / T-2 in the workflow.json task
// T-CMD-LINE-SURFACE-1.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resolveTestBinary always builds a fresh binary from the current source tree
// so the subprocess tests exercise the code under test (including the JQVars
// fix), not whatever binary happens to be installed at $HOME/.local/bin/browzer.
//
// The build result is cached at a stable path within the test's temporary
// directory so a single TestXxx invocation that calls resolveTestBinary twice
// pays the build cost only once.
func resolveTestBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "browzer")
	modulePath, err := findModuleRoot(t)
	if err != nil {
		t.Fatalf("resolveTestBinary: cannot find module root: %v", err)
	}
	t.Logf("resolveTestBinary: building binary from %s → %s", modulePath, bin)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/browzer")
	cmd.Dir = modulePath
	out, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Fatalf("go build failed: %v\n%s", buildErr, out)
	}
	return bin
}

// findModuleRoot walks upward from the test working directory until it finds a
// go.mod file and returns that directory.
func findModuleRoot(t *testing.T) (string, error) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found walking up from %s", dir)
		}
		dir = parent
	}
}

// subprocessTestEnv prepares an isolated environment for subprocess invocations.
// It creates a temp root directory, overrides HOME to prevent collisions with
// the user's real ~/.browzer state, pre-creates the ~/.browzer directory that
// the daemon's PID-write path requires, and sets BROWZER_DAEMON_SOCKET.
//
// Returns (testRoot, sock, env) where:
//   - testRoot is the temp directory that will be removed by t.Cleanup.
//   - sock is the full path of the isolated Unix socket.
//   - env is the []string slice to pass to exec.Cmd.Env.
func subprocessTestEnv(t *testing.T) (testRoot, sock string, env []string) {
	t.Helper()
	root, err := os.MkdirTemp("", "brz-sub-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	// The daemon's writePID call creates ~/.browzer/daemon.pid.
	// With HOME overridden, that becomes root/.browzer/daemon.pid.
	if err := os.MkdirAll(filepath.Join(root, ".browzer"), 0o755); err != nil {
		t.Fatal(err)
	}

	sockPath := filepath.Join(root, "browzer.sock")
	dataHome := filepath.Join(root, "data")

	// Build env: carry through the host env (needed for Go runtime),
	// then override the isolation variables.
	base := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		// Strip any HOME / BROWZER_DAEMON_SOCKET already set on the host
		// so our overrides below are the sole values.
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "BROWZER_DAEMON_SOCKET=") {
			continue
		}
		base = append(base, kv)
	}
	base = append(base,
		"HOME="+root,
		"BROWZER_DAEMON_SOCKET="+sockPath,
		"XDG_DATA_HOME="+dataHome,
	)
	return root, sockPath, base
}

// startSubprocessDaemon launches `<bin> daemon start` (foreground) in a
// background goroutine using env, waits until `<bin> daemon status` exits 0,
// and registers a t.Cleanup to stop the daemon. It fatals if the daemon is
// not ready within 15 seconds.
func startSubprocessDaemon(t *testing.T, bin string, env []string) {
	t.Helper()

	daemonCmd := exec.Command(bin, "daemon", "start")
	daemonCmd.Env = env
	var daemonStderr strings.Builder
	daemonCmd.Stderr = &daemonStderr

	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}

	// Register cleanup: graceful stop first, then wait / kill.
	t.Cleanup(func() {
		stopCmd := exec.Command(bin, "daemon", "stop")
		stopCmd.Env = env
		_ = stopCmd.Run()
		done := make(chan error, 1)
		go func() { done <- daemonCmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = daemonCmd.Process.Kill()
		}
	})

	// Poll until daemon is ready.
	deadline := time.Now().Add(15 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		statusCmd := exec.Command(bin, "daemon", "status")
		statusCmd.Env = env
		if err := statusCmd.Run(); err == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("daemon did not become ready within 15s\ndaemon stderr:\n%s", daemonStderr.String())
	}
}

// seedWorkflowV2 writes a valid schema-v2 workflow.json to path.
// featureId must match the pattern ^feat-[0-9]{8}-[a-z0-9-]+$ enforced by the
// CUE schema. The seed is the minimal struct that passes daemon-side validation
// without any steps.
func seedWorkflowV2(t *testing.T, path string) {
	t.Helper()
	const content = `{
  "schemaVersion": 2,
  "pluginVersion": null,
  "featureId": "feat-20260504-jqvars-roundtrip",
  "featureName": "JQ Vars Roundtrip Test",
  "featDir": "docs/browzer/feat-20260504-jqvars-roundtrip",
  "originalRequest": "subprocess test seed",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-04T00:00:00Z"},
  "startedAt": "2026-05-04T00:00:00Z",
  "updatedAt": "2026-05-04T00:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seedWorkflowV2: %v", err)
	}
}

// TestWorkflowPatch_DaemonJQVarsRoundtrip is the T-1 testSpec from the
// CMD-LINE-SURFACE task in workflow.json.
//
// It invokes the real CLI binary as a subprocess with a live daemon and asserts
// that `browzer workflow patch --arg KEY=hello --jq '.featureName = $KEY' --await`
// routes through daemon-sync (not fallback-sync) AND that the resulting
// workflow.json has .featureName == "hello".
//
// This test covers the WF-SYNC-1 JQVars fix in workflow_mutator_helpers.go:
// prior to the fix, JQVars was NOT forwarded to daemon.WorkflowMutateParams,
// so gojq would error with "undefined variable: $KEY" inside the daemon,
// causing a daemon error → fallback-sync instead of daemon-sync.
func TestWorkflowPatch_DaemonJQVarsRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess + daemon — long-running")
	}

	bin := resolveTestBinary(t)
	testRoot, _, env := subprocessTestEnv(t)
	startSubprocessDaemon(t, bin, env)

	// Seed a valid v2 workflow.json.
	wfPath := filepath.Join(testRoot, "workflow.json")
	seedWorkflowV2(t, wfPath)

	// Invoke the bug-trigger command:
	//   browzer workflow patch --workflow <wf> --arg KEY=hello --jq '.featureName = $KEY' --await
	patchCmd := exec.Command(bin,
		"workflow", "patch",
		"--workflow", wfPath,
		"--arg", "KEY=hello",
		"--jq", ".featureName = $KEY",
		"--await",
	)
	patchCmd.Env = env
	var patchStdout, patchStderr strings.Builder
	patchCmd.Stdout = &patchStdout
	patchCmd.Stderr = &patchStderr

	if err := patchCmd.Run(); err != nil {
		t.Fatalf("patch exited non-zero: %v\nstdout: %s\nstderr: %s",
			err, patchStdout.String(), patchStderr.String())
	}

	stderrOut := patchStderr.String()
	t.Logf("patch stderr: %s", stderrOut)

	// Assert mode=daemon-sync in the audit line.
	if !strings.Contains(stderrOut, "mode=daemon-sync") {
		t.Errorf("expected 'mode=daemon-sync' in stderr\nstderr: %s", stderrOut)
	}
	// Assert NOT fallback — fallback means JQVars were not forwarded.
	if strings.Contains(stderrOut, "mode=fallback-sync") {
		t.Errorf("got 'mode=fallback-sync' — JQVars likely not forwarded to daemon\nstderr: %s", stderrOut)
	}
	// Guard against the pre-fix "undefined variable" error.
	if strings.Contains(stderrOut, "undefined variable") {
		t.Errorf("'undefined variable' in stderr — JQVars not forwarded\nstderr: %s", stderrOut)
	}

	// Assert the resulting workflow.json has .featureName == "hello".
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("read workflow.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow.json: %v\ncontent: %s", err, data)
	}
	featureName, _ := doc["featureName"].(string)
	if featureName != "hello" {
		t.Errorf(".featureName = %q; want %q\ncontent: %s", featureName, "hello", data)
	}
}

// TestWorkflowPatch_DaemonJQVarsRoundtrip_ArgJSON is the T-2 companion test.
//
// It exercises `--argjson NUM=42 --jq '.totalElapsedMin = $NUM'` — the
// JSON-parsed variant of the JQVars fix. Without the fix, the daemon would
// receive an empty JQVars map and gojq would error with
// "undefined variable: $NUM", falling back to standalone-sync.
func TestWorkflowPatch_DaemonJQVarsRoundtrip_ArgJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess + daemon — long-running")
	}

	bin := resolveTestBinary(t)
	testRoot, _, env := subprocessTestEnv(t)
	startSubprocessDaemon(t, bin, env)

	wfPath := filepath.Join(testRoot, "workflow.json")
	seedWorkflowV2(t, wfPath)

	// Invoke: patch --argjson NUM=42 --jq '.totalElapsedMin = $NUM' --await
	patchCmd := exec.Command(bin,
		"workflow", "patch",
		"--workflow", wfPath,
		"--argjson", "NUM=42",
		"--jq", ".totalElapsedMin = $NUM",
		"--await",
	)
	patchCmd.Env = env
	var patchStdout, patchStderr strings.Builder
	patchCmd.Stdout = &patchStdout
	patchCmd.Stderr = &patchStderr

	if err := patchCmd.Run(); err != nil {
		t.Fatalf("patch exited non-zero: %v\nstdout: %s\nstderr: %s",
			err, patchStdout.String(), patchStderr.String())
	}

	stderrOut := patchStderr.String()
	t.Logf("patch stderr: %s", stderrOut)

	// Assert daemon-sync mode.
	if !strings.Contains(stderrOut, "mode=daemon-sync") {
		t.Errorf("expected 'mode=daemon-sync' in stderr\nstderr: %s", stderrOut)
	}
	if strings.Contains(stderrOut, "mode=fallback-sync") {
		t.Errorf("got 'mode=fallback-sync' — JQVars not forwarded\nstderr: %s", stderrOut)
	}
	if strings.Contains(stderrOut, "undefined variable") {
		t.Errorf("'undefined variable' in stderr — JQVars not forwarded\nstderr: %s", stderrOut)
	}

	// Assert .totalElapsedMin == 42 (number).
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("read workflow.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse workflow.json: %v\ncontent: %s", err, data)
	}
	// JSON numbers unmarshal as float64.
	elapsed, _ := doc["totalElapsedMin"].(float64)
	if elapsed != 42 {
		t.Errorf(".totalElapsedMin = %v; want 42\ncontent: %s", doc["totalElapsedMin"], data)
	}
}
