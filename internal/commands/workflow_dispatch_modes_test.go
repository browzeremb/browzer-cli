package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/daemon"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// startInProcessDaemon stands up a daemon.Server in the current process,
// listens on a /tmp socket, and points BROWZER_DAEMON_SOCKET at it for the
// duration of the test (via t.Setenv). Returns the socket path + a cleanup.
func startInProcessDaemon(t *testing.T) string {
	t.Helper()
	sockDir, err := os.MkdirTemp("/tmp", "brz-cli-mode-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "d.sock")

	srv := daemon.NewServer(daemon.Options{
		SocketPath:        sock,
		WorkflowKeepalive: 30 * time.Minute,
	})

	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		srv.Stop()
	})

	// Spin until the daemon is ready.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := daemon.NewClient(sock).Health(t.Context())
		if err == nil {
			_ = c
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Setenv(config.EnvDaemonSocket, sock)
	return sock
}

// TestAppendStep_ConcurrencyN8AcrossModes verifies the N=8 no-lost-writes
// contract across all three dispatch modes:
//
//	standalone    — historic in-process path (--sync flag).
//	daemon-async  — fire-and-forget through the daemon (--async flag).
//	daemon-sync   — sync via daemon (--await flag), durable on each call.
//
// The standalone case is also covered by TestAppendStep_ConcurrencyN8NoLostWrites
// in workflow_append_step_test.go; this table-driven version is the
// regression net for the daemon paths added in 2026-04-29.
//
// For daemon-async, we fire 8 concurrent calls then drain by issuing a
// single --await call to the same path; that --await blocks until the FIFO
// has flushed everything before it, so on completion all 8 prior writes
// are guaranteed durable.
func TestAppendStep_ConcurrencyN8AcrossModes(t *testing.T) {
	// TASK_02 / WF-SYNC-1 bypass: this test fixture predates the CUE
	// schema cutover (featureId=`feat-test`, schemaVersion=1, missing
	// required step fields). The test exercises dispatch concurrency,
	// not schema enforcement, so set the bypass at the parent level
	// (subtest goroutines forbid t.Setenv but inherit the parent env).
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	type mode struct {
		name string
		flag string // "" for standalone (default sync via env), "--await", "--async"
	}
	modes := []mode{
		{name: "standalone", flag: "--sync"},
		{name: "daemon-async", flag: "--async"},
		{name: "daemon-sync", flag: "--await"},
	}

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			if m.name != "standalone" {
				_ = startInProcessDaemon(t)
			}
			wfPath := writeWorkflowFile(t, minimalWorkflowJSON)

			const N = 8
			var wg sync.WaitGroup
			errs := make([]error, N)

			for i := range N {
				wg.Add(1)
				go func(n int) {
					defer wg.Done()
					var stdout, stderr bytes.Buffer
					// NOTE: buildWorkflowCommand (not …T) — the parallel
					// goroutines inside this test forbid t.Setenv. The mode
					// is set explicitly via the flag in `args` below
					// (--sync / --async / --await), so we don't need the
					// BROWZER_WORKFLOW_MODE env override here.
					root := buildWorkflowCommand(&stdout, &stderr)

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
						errs[n] = err
						return
					}

					args := []string{
						"workflow", "append-step",
						"--payload", payloadFile,
						"--workflow", wfPath,
					}
					if m.flag != "" {
						args = append(args, m.flag)
					}
					root.SetArgs(args)
					errs[n] = root.Execute()
				}(i)
			}
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					t.Errorf("goroutine %d (%s): unexpected error: %v", i, m.name, err)
				}
			}

			// daemon-async: drain by sending a no-op-ish --await call. We
			// use update-step on a known-existing step (the seed has none —
			// instead we use append-step for a sentinel step then verify N+1).
			// Simpler: re-issue a sync call that triggers a flush.
			if m.name == "daemon-async" {
				// Fire one --await append on the same path. The FIFO is
				// per-path; this call blocks until all 8 prior writes
				// finish and flushes them durably.
				sentinelStep := `{
				  "stepId": "STEP_SENTINEL",
				  "name": "TASK",
				  "taskId": "TASK_SENTINEL",
				  "status": "PENDING",
				  "applicability": {"applicable": true, "reason": "default"},
				  "completedAt": null,
				  "owner": null,
				  "worktrees": {"used": false, "worktrees": []},
				  "task": {}
				}`
				payloadFile := filepath.Join(t.TempDir(), "sentinel.json")
				if err := os.WriteFile(payloadFile, []byte(sentinelStep), 0o644); err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				root := buildWorkflowCommandT(t, &stdout, &stderr)
				root.SetArgs([]string{
					"workflow", "append-step",
					"--payload", payloadFile,
					"--workflow", wfPath,
					"--await",
				})
				if err := root.Execute(); err != nil {
					t.Fatalf("sentinel --await flush: %v\nstderr: %s", err, stderr.String())
				}
			}

			// Read back and verify.
			data, err := os.ReadFile(wfPath)
			if err != nil {
				t.Fatal(err)
			}
			var doc wf.Workflow
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatalf("parse: %v", err)
			}

			expected := N
			if m.name == "daemon-async" {
				expected = N + 1 // includes the sentinel
			}
			if len(doc.Steps) != expected {
				t.Errorf("expected %d steps in %s mode, got %d", expected, m.name, len(doc.Steps))
			}

			// No two steps may share the same stepId.
			seen := map[string]int{}
			for _, s := range doc.Steps {
				seen[s.StepID]++
				if seen[s.StepID] > 1 {
					t.Errorf("duplicate stepId %q in %s mode — write was lost", s.StepID, m.name)
				}
			}
		})
	}
}
