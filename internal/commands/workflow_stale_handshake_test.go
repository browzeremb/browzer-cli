package commands

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/config"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// TestStaleHandshake_FallsBackToStandalone exercises the protocol-version
// preflight (WF-SYNC-1, 2026-05-04). A stale daemon binary that advertises
// `workflow.v1` (so HasCapability passes) but reports `protocolVersion: 1`
// over `Daemon.Version` MUST cause the CLI to:
//
//  1. Emit a one-shot stderr warning naming both peers' protocol versions.
//  2. Fall back to the standalone path so the mutation still lands.
//
// The contract is the difference between "silent corruption" (old code)
// and "explicit fallback" (new code) — drift detection at preflight is
// the entire reason the handshake exists. Loss of either property is a
// regression.
//
// QA-002 (2026-05-04): SEQUENTIAL BY DESIGN. This test reassigns the
// package-level `sync.Once` latches `daemonFallbackWarnOnce` and
// `daemonVersionMismatchWarnOnce` to fresh literals so it can observe a
// "first-time" warning emission. The reassignment is safe ONLY when this
// test (and any sibling that touches those latches) runs sequentially.
//
//	DO NOT add t.Parallel() to this test, or to any sibling test in this
//	package that touches the warn-once latches. Doing so will introduce a
//	data race the race detector will flag.
//
// We intentionally do not add a `runtime.GOMAXPROCS(1)` enforcement helper
// because it would also penalize unrelated parallel sub-tests; the comment
// is the load-bearing guard.
func TestStaleHandshake_FallsBackToStandalone(t *testing.T) {
	// Bypass the CUE schema validator: this fixture predates the v2
	// cutover. The test exercises dispatch fallback, not schema enforcement.
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	// Clear BROWZER_WORKFLOW_MODE so the dispatcher attempts the daemon
	// path. The test passes --async on the command line to be explicit.
	t.Setenv("BROWZER_WORKFLOW_MODE", "")

	// Stand up the stub daemon BEFORE the cobra command runs, so the
	// EnvDaemonSocket override propagates into Client construction.
	sockDir, err := os.MkdirTemp("/tmp", "brz-stale-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "d.sock")

	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go runStaleStubDaemon(listener)

	t.Setenv(config.EnvDaemonSocket, sock)

	// Reset the warn-once latches so this test sees the first emission.
	// (Previously-running tests in the same package may have already
	// triggered them; sync.Once is process-scoped, and reassignment to
	// a fresh literal is the simplest way to give the test a clean slate.
	// Tests in this package run sequentially — no t.Parallel — so
	// reassignment is safe.)
	daemonFallbackWarnOnce = sync.Once{}
	daemonVersionMismatchWarnOnce = sync.Once{}

	// Write a minimal workflow.json the standalone path can mutate.
	wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

	// Build a cobra root WITHOUT buildWorkflowCommandT's --sync forcing —
	// we want the dispatcher to attempt the daemon path, hit the stale
	// preflight, and fall back to standalone of its own accord.
	var stdout, stderr bytes.Buffer
	root := buildWorkflowCommand(&stdout, &stderr)

	// Append a step via the dispatcher with --async (daemon path preferred).
	stepID := "STEP_00_TASK"
	payload := `{
	  "stepId": "STEP_00_TASK",
	  "name": "TASK",
	  "taskId": "TASK_01",
	  "status": "PENDING",
	  "applicability": {"applicable": true, "reason": "default"},
	  "completedAt": null,
	  "owner": null,
	  "worktrees": {"used": false, "worktrees": []},
	  "task": {}
	}`
	payloadFile := filepath.Join(t.TempDir(), "step.json")
	if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	root.SetArgs([]string{
		"workflow", "append-step",
		"--payload", payloadFile,
		"--workflow", wfPath,
		"--async",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	// Assertion 1: stderr contains the protocol-mismatch warning.
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "daemon protocol mismatch") {
		t.Errorf("expected stderr to contain 'daemon protocol mismatch', got:\n%s", stderrStr)
	}
	if !strings.Contains(stderrStr, "expected v2") || !strings.Contains(stderrStr, "got v1") {
		t.Errorf("expected stderr to name both protocol versions, got:\n%s", stderrStr)
	}

	// Assertion 2: the workflow.json was actually mutated via the standalone
	// path (the new step is present + total counters bumped).
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Steps) != 1 {
		t.Fatalf("expected 1 step (mutation must have applied via standalone fallback), got %d", len(doc.Steps))
	}
	if doc.Steps[0].StepID != stepID {
		t.Errorf("steps[0].stepId = %q, want %q", doc.Steps[0].StepID, stepID)
	}

	// Assertion 3: the audit line came back with mode=fallback-sync and
	// reason=daemon_protocol_mismatch (proves the dispatch decision branch
	// was taken, not just any fallback path).
	if !strings.Contains(stderrStr, "mode=fallback-sync") {
		t.Errorf("expected audit line mode=fallback-sync, got:\n%s", stderrStr)
	}
	if !strings.Contains(stderrStr, "reason=daemon_protocol_mismatch") {
		t.Errorf("expected audit line reason=daemon_protocol_mismatch, got:\n%s", stderrStr)
	}
}

// runStaleStubDaemon answers the two methods the dispatcher needs:
//
//   - Health: returns capabilities=["workflow.v1"] so HasCapability passes.
//   - Daemon.Version: returns protocolVersion=1 (stale).
//
// Any other method gets `method_not_found` so a misrouted RPC fails loudly
// rather than masquerading as success. The handler exits when the listener
// closes (test cleanup).
func runStaleStubDaemon(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 8192)
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(buf[:n], &req); err != nil {
				return
			}
			var result any
			switch req.Method {
			case "Health":
				result = map[string]any{
					"uptimeSec":    1,
					"queueLen":     0,
					"dbPath":       "/dev/null",
					"capabilities": []string{"workflow.v1"},
				}
			case "Daemon.Version":
				// The stale-binary scenario: daemon answers the handshake
				// but reports an older protocol version. CLI must fall back.
				result = map[string]any{
					"daemonVersion":    "1.5.0",
					"schemaVersion":    1,
					"protocolFeatures": []string{"jqVars"},
					"protocolVersion":  1,
				}
			default:
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32601, "message": "method_not_found: " + req.Method},
				}
				body, _ := json.Marshal(resp)
				_, _ = c.Write(append(body, '\n'))
				return
			}
			resp := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
			body, _ := json.Marshal(resp)
			_, _ = c.Write(append(body, '\n'))
		}(conn)
	}
}
