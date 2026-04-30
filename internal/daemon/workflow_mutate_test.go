package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// minimalWorkflowJSON is the smallest schema-v1 workflow.json the validator
// accepts. Tests in this file write copies of this to disk and let the
// daemon mutate them.
const minimalWorkflowJSON = `{
  "schemaVersion": 1,
  "featureId": "feat-mutate-test",
  "featureName": "WorkflowMutate Test",
  "featDir": "docs/browzer/feat-mutate-test",
  "originalRequest": "test",
  "operator": {"locale": "pt-BR"},
  "config": {"mode": "autonomous", "setAt": "2026-04-29T00:00:00Z"},
  "startedAt": "2026-04-29T00:00:00Z",
  "updatedAt": "2026-04-29T00:00:00Z",
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}`

// startTestDaemon stands up a Server with WorkflowMutate wired, listening
// on a per-test socket under /tmp/brz-wfm-*.sock. Returns the client and a
// cleanup. The Server is created with IdleTimeout=0 so its idleWatcher is
// inert (tests want explicit lifecycle control).
//
// keepalive controls the per-path drainer's idle timeout; pass a small
// duration (e.g. 200ms) to exercise the idle-collector test, or 30min for
// "never collect during test".
func startTestDaemon(t *testing.T, keepalive time.Duration) (*Client, *Server, func()) {
	t.Helper()
	sockDir, err := os.MkdirTemp("/tmp", "brz-wfm-*")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sockDir, "d.sock")
	srv := NewServer(Options{
		SocketPath:        sock,
		WorkflowKeepalive: keepalive,
	})
	// Workflow handler is registered by NewServer → handlers map. Wire is
	// optional (it adds Read/Track/SessionRegister stubs). Since tests for
	// WorkflowMutate don't depend on Read/Track, we skip Wire to avoid the
	// nil-deps panic; handleWorkflowMutate is independent.

	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = srv.Serve(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cli := NewClient(sock)
	cleanup := func() {
		cancel()
		srv.Stop()
		_ = os.RemoveAll(sockDir)
	}
	return cli, srv, cleanup
}

func writeMinimalWorkflow(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(p, []byte(minimalWorkflowJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestWorkflowMutate_QueueOrdering fires N=20 sequential append-step
// mutations through the daemon's --await path. The drainer's per-path FIFO
// must execute them in order; the resulting workflow.steps array must
// contain exactly N entries with stepIds in submission order.
//
// We use --await (sync) here so each call blocks on completion before the
// next is sent — that's the sequential FIFO contract. The async case is
// covered by the daemon-async branch of the migrated concurrency test.
func TestWorkflowMutate_QueueOrdering(t *testing.T) {
	cli, _, cleanup := startTestDaemon(t, 30*time.Minute)
	defer cleanup()

	wfPath := writeMinimalWorkflow(t)

	const N = 20
	for i := 0; i < N; i++ {
		stepID := fmt.Sprintf("STEP_%02d", i)
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
		}`, stepID, i)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		res, err := cli.WorkflowMutate(ctx, WorkflowMutateParams{
			Verb:            "append-step",
			Path:            wfPath,
			Payload:         json.RawMessage(payload),
			AwaitDurability: true,
			LockTimeoutMs:   5000,
			WriteID:         stepID,
		})
		cancel()
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if res.Mode != "daemon-sync" {
			t.Errorf("call %d mode = %q, want daemon-sync", i, res.Mode)
		}
		if !res.ValidatedOk {
			t.Errorf("call %d: validatedOk=false", i)
		}
		if !res.Durable {
			t.Errorf("call %d: durable=false (awaitDurability=true should fsync)", i)
		}
	}

	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Steps) != N {
		t.Fatalf("expected %d steps, got %d", N, len(doc.Steps))
	}
	for i, s := range doc.Steps {
		want := fmt.Sprintf("STEP_%02d", i)
		if s.StepID != want {
			t.Errorf("steps[%d].stepId = %q, want %q (FIFO violated)", i, s.StepID, want)
		}
	}
}

// TestWorkflowMutate_DaemonCrashMidWrite is a coarse-grained crash-safety
// assertion. We can't SIGKILL ourselves mid-fsync inside the test process,
// but we CAN verify the atomic-write contract holds by:
//
//  1. Run a sync mutation; verify file is consistent.
//  2. Stop the daemon mid-stream of pending async mutations.
//  3. Re-read the file; assert it parses + validates (not torn).
//
// The atomic guarantee (CreateTemp + Rename) means the file is either the
// pre-write contents or the post-write contents — never a partial mix.
func TestWorkflowMutate_DaemonCrashMidWrite(t *testing.T) {
	cli, srv, cleanup := startTestDaemon(t, 30*time.Minute)
	defer cleanup()

	wfPath := writeMinimalWorkflow(t)

	// Step 1: one sync write to anchor the file.
	payload := `{
	  "stepId": "STEP_ANCHOR",
	  "name": "TASK",
	  "taskId": "TASK_ANCHOR",
	  "status": "PENDING",
	  "applicability": {"applicable": true, "reason": "default"},
	  "completedAt": null,
	  "owner": null,
	  "worktrees": {"used": false, "worktrees": []},
	  "task": {}
	}`
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	if _, err := cli.WorkflowMutate(ctx, WorkflowMutateParams{
		Verb:            "append-step",
		Path:            wfPath,
		Payload:         json.RawMessage(payload),
		AwaitDurability: true,
		LockTimeoutMs:   5000,
	}); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()

	// Step 2: fire 5 async mutations, then kill the daemon.
	for i := 0; i < 5; i++ {
		stepID := fmt.Sprintf("STEP_PEND_%02d", i)
		p := fmt.Sprintf(`{
		  "stepId": %q,
		  "name": "TASK",
		  "taskId": "TASK_%02d",
		  "status": "PENDING",
		  "applicability": {"applicable": true, "reason": "default"},
		  "completedAt": null,
		  "owner": null,
		  "worktrees": {"used": false, "worktrees": []},
		  "task": {}
		}`, stepID, i)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		if _, err := cli.WorkflowMutate(ctx, WorkflowMutateParams{
			Verb:            "append-step",
			Path:            wfPath,
			Payload:         json.RawMessage(p),
			AwaitDurability: false,
			LockTimeoutMs:   5000,
		}); err != nil {
			cancel()
			t.Fatalf("async call %d: %v", i, err)
		}
		cancel()
	}

	// Hard-stop the daemon mid-flight. Some async writes will land on disk;
	// some will be silently dropped. Both are acceptable.
	srv.Stop()
	// Give in-flight drainer goroutines a brief window to finish their
	// current write so we don't trip the TempDir cleanup on lingering
	// .tmp files. The test asserts atomicity, not zero-tail-latency.
	time.Sleep(200 * time.Millisecond)

	// Step 3: file must still parse + validate. No torn writes allowed.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc wf.Workflow
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("file is torn / unparseable after daemon crash: %v\n%s", err, data)
	}
	if errs := wf.Validate(doc); len(errs) > 0 {
		t.Fatalf("file failed validation after daemon crash: %v", errs)
	}
	// Anchor write must have survived.
	if doc.TotalSteps < 1 {
		t.Fatalf("expected anchor step to have survived, got totalSteps=%d", doc.TotalSteps)
	}
}

// TestWorkflowMutate_FallbackOnMissingCapability stands up a stub daemon
// that responds to Health with an empty Capabilities list and to
// WorkflowMutate with method_not_found. The test directly exercises
// HasCapability + WorkflowMutate to assert the client surfaces the
// failures — the actual fallback (running standalone) lives in the
// commands package and is exercised by TestAppendStep_ConcurrencyN8NoLostWrites
// running in standalone mode.
func TestWorkflowMutate_FallbackOnMissingCapability(t *testing.T) {
	sockDir, err := os.MkdirTemp("/tmp", "brz-stub-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")

	// Stub daemon: Health returns capabilities=[], WorkflowMutate returns
	// method_not_found.
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go runStubServer(l, map[string]any{
		"Health": map[string]any{
			"uptimeSec":    1,
			"queueLen":     0,
			"dbPath":       "/dev/null",
			"capabilities": []string{},
		},
	})

	cli := NewClient(sock)
	if cli.HasCapability(t.Context(), "workflow.v1") {
		t.Fatal("HasCapability(workflow.v1) = true, want false (stub advertises no caps)")
	}
	// And a direct WorkflowMutate must surface method_not_found.
	_, err = cli.WorkflowMutate(t.Context(), WorkflowMutateParams{
		Verb: "append-step",
		Path: "/tmp/wf.json",
	})
	if err == nil {
		t.Fatal("WorkflowMutate against stub should error (method_not_found)")
	}
}

// runStubServer accepts JSON-RPC requests on l and replies to known methods
// from `responses`. Unknown methods get a method_not_found error.
func runStubServer(l net.Listener, responses map[string]any) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			buf := make([]byte, 4096)
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
			var resp map[string]any
			if r, ok := responses[req.Method]; ok {
				resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": r}
			} else {
				resp = map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32601, "message": "method_not_found: " + req.Method},
				}
			}
			body, _ := json.Marshal(resp)
			_, _ = c.Write(append(body, '\n'))
		}(conn)
	}
}

// TestWorkflowMutate_CapabilityCacheTTL injects a controllable clock into
// Client.now and verifies that:
//   - First HasCapability call hits Health (1 server round-trip).
//   - Second call within 60s of the first uses the cache (0 round-trips).
//   - Third call after advancing the clock past 60s hits Health again.
func TestWorkflowMutate_CapabilityCacheTTL(t *testing.T) {
	cli, _, cleanup := startTestDaemon(t, 30*time.Minute)
	defer cleanup()

	// Inject the clock. We start at t=0 and advance manually.
	var clockNow atomic.Int64
	clockNow.Store(time.Now().UnixNano())
	cli.now = func() time.Time {
		return time.Unix(0, clockNow.Load())
	}

	// Wrap dialOnce by counting Health calls. Simplest: count via a
	// goroutine-local atomic incremented inside a custom HTTP-equivalent.
	// Since dialOnce is package-private, instead we measure indirectly by
	// observing that the third call DOES return true (because real daemon
	// responds with workflow.v1) — combined with the time-progression
	// assertion, this proves the cache TTL boundary works.

	// First call → fresh fetch.
	if !cli.HasCapability(t.Context(), "workflow.v1") {
		t.Fatal("first HasCapability returned false; daemon should advertise workflow.v1")
	}
	firstCapsAt := cli.capsAt
	if firstCapsAt.IsZero() {
		t.Fatal("first call did not populate capsAt")
	}

	// Second call within TTL → cache hit. capsAt must NOT change.
	clockNow.Store(clockNow.Load() + int64(30*time.Second))
	_ = cli.HasCapability(t.Context(), "workflow.v1")
	if !cli.capsAt.Equal(firstCapsAt) {
		t.Fatalf("second call updated capsAt — should have been cache hit (was %v, now %v)", firstCapsAt, cli.capsAt)
	}

	// Third call past TTL → fresh fetch. capsAt must advance.
	clockNow.Store(clockNow.Load() + int64(61*time.Second))
	_ = cli.HasCapability(t.Context(), "workflow.v1")
	if cli.capsAt.Equal(firstCapsAt) {
		t.Fatalf("third call did not refresh capsAt past TTL boundary")
	}
}

// TestWorkflowMutate_QueueIdleDrainerExit fires one mutation, waits past the
// queue keepalive, and asserts the per-path drainer goroutine has exited
// (activeDrainers atomic decremented to zero).
func TestWorkflowMutate_QueueIdleDrainerExit(t *testing.T) {
	keepalive := 200 * time.Millisecond
	cli, srv, cleanup := startTestDaemon(t, keepalive)
	defer cleanup()

	wfPath := writeMinimalWorkflow(t)
	payload := `{
	  "stepId": "STEP_ONLY",
	  "name": "TASK",
	  "taskId": "TASK_01",
	  "status": "PENDING",
	  "applicability": {"applicable": true, "reason": "default"},
	  "completedAt": null,
	  "owner": null,
	  "worktrees": {"used": false, "worktrees": []},
	  "task": {}
	}`
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	if _, err := cli.WorkflowMutate(ctx, WorkflowMutateParams{
		Verb:            "append-step",
		Path:            wfPath,
		Payload:         json.RawMessage(payload),
		AwaitDurability: true,
		LockTimeoutMs:   5000,
	}); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()

	if got := srv.workflowDispatcher.activeDrainers.Load(); got != 1 {
		t.Fatalf("expected activeDrainers=1 right after first mutation, got %d", got)
	}

	// Wait past the idle deadline + a generous buffer for scheduler jitter.
	deadline := time.Now().Add(keepalive*4 + time.Second)
	for time.Now().Before(deadline) {
		if srv.workflowDispatcher.activeDrainers.Load() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := srv.workflowDispatcher.activeDrainers.Load(); got != 0 {
		t.Fatalf("drainer did not exit after keepalive; activeDrainers=%d", got)
	}
}

// TestWorkflowMutate_NoLockRejectedInDaemonPath verifies that a request
// with noLock=true is rejected by the daemon BEFORE any queue contact.
// The error string must be `noLock_unsupported_in_daemon_path` so the
// CLI can map it to a fallback decision.
func TestWorkflowMutate_NoLockRejectedInDaemonPath(t *testing.T) {
	cli, _, cleanup := startTestDaemon(t, 30*time.Minute)
	defer cleanup()

	wfPath := writeMinimalWorkflow(t)
	_, err := cli.WorkflowMutate(t.Context(), WorkflowMutateParams{
		Verb:   "set-current-step",
		Path:   wfPath,
		Args:   []string{"step-1"},
		NoLock: true,
	})
	if err == nil {
		t.Fatal("expected error for noLock=true, got nil")
	}
	if want := "noLock_unsupported_in_daemon_path"; !errStringContains(err, want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

// TestWorkflowMutate_QueueFullBackpressure fills the per-path queue to
// capacity (64) by enqueuing async writes faster than the drainer can
// process them, then asserts the 65th enqueue returns queue_full.
//
// To pin the drainer mid-job, we wedge a path-lock holder OUTSIDE the
// daemon: take a wf.Lock on the path before any RPC fires. The drainer
// will block on Acquire indefinitely; meanwhile the queue fills up.
func TestWorkflowMutate_QueueFullBackpressure(t *testing.T) {
	cli, _, cleanup := startTestDaemon(t, 30*time.Minute)
	defer cleanup()

	wfPath := writeMinimalWorkflow(t)

	// Hold the file lock from outside the daemon. The drainer will block
	// trying to Acquire, so the channel won't drain.
	lock, err := wf.NewLock(wfPath, 30*time.Second, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()

	// Fire pathQueueCap+1 (65) async appends — first one is handed off to
	// the drainer (now stuck on Acquire), next 64 fill the buffered channel.
	// The 66th call must return queue_full.
	for i := 0; i <= pathQueueCap; i++ { // 0..64 inclusive = 65 calls
		p := fmt.Sprintf(`{"stepId":"STEP_%03d","name":"TASK","taskId":"T_%d","status":"PENDING","applicability":{"applicable":true},"completedAt":null,"owner":null,"worktrees":{"used":false},"task":{}}`, i, i)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		if _, err := cli.WorkflowMutate(ctx, WorkflowMutateParams{
			Verb:            "append-step",
			Path:            wfPath,
			Payload:         json.RawMessage(p),
			AwaitDurability: false,
			LockTimeoutMs:   500, // small so the drainer's eventual Acquire fails fast
		}); err != nil {
			cancel()
			t.Fatalf("async call %d should succeed (queue not yet full): %v", i, err)
		}
		cancel()
	}

	// 66th call — channel is at capacity AND the drainer is parked on
	// Acquire (not selecting on q.ch), so neither buffer nor handoff can
	// accept the send. enqueue's `select default` fires queue_full.
	p := `{"stepId":"STEP_FULL","name":"TASK","taskId":"T_FULL","status":"PENDING","applicability":{"applicable":true},"completedAt":null,"owner":null,"worktrees":{"used":false},"task":{}}`
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = cli.WorkflowMutate(ctx, WorkflowMutateParams{
		Verb:            "append-step",
		Path:            wfPath,
		Payload:         json.RawMessage(p),
		AwaitDurability: false,
		LockTimeoutMs:   500,
	})
	if err == nil {
		t.Fatal("66th call should have returned queue_full, got nil")
	}
	if !errStringContains(err, "queue_full") {
		t.Fatalf("expected queue_full error, got %q", err)
	}
}

// errStringContains is a tiny helper avoiding the strings import for one call.
func errStringContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// _ pulls in errors so unused-import doesn't complain in environments where
// the package no-op compile passes need to see it; keep the silent import.
var _ = errors.New
var _ sync.Mutex

// TestWorkflowMutateParams_AdditiveJQVarsContract (QA-11 + RT-1 wire portion)
// — pins the additive JSON-RPC contract for JQVars: a NEW CLI sending
// `jqVars` to a daemon binary STRUCTURALLY OLDER than 1.6.0 (no JQVars field
// in the local copy of WorkflowMutateParams) must NOT error on json.Unmarshal.
// The unknown field is silently dropped; the daemon proceeds to call ApplyJQ
// without bind variables, and gojq fails downstream with "undefined variable
// $id" — which is the documented user-facing behavior (see CHANGELOG +
// JQVars docstring + workflow_patch.go RunE hint).
//
// This test guards against a future tightening of the daemon's decoder
// (e.g. adding json.NewDecoder().DisallowUnknownFields()) which would silently
// break new-CLI ↔ old-daemon pairings.
func TestWorkflowMutateParams_AdditiveJQVarsContract(t *testing.T) {
	// Structurally OLDER copy of the params struct: same JSON tags but
	// missing the JQVars field. Simulates a v1.5.x daemon binary.
	type oldParams struct {
		Verb            string          `json:"verb"`
		Path            string          `json:"path"`
		Payload         json.RawMessage `json:"payload,omitempty"`
		Args            []string        `json:"args,omitempty"`
		JQExpr          string          `json:"jqExpr,omitempty"`
		NoLock          bool            `json:"noLock,omitempty"`
		AwaitDurability bool            `json:"awaitDurability,omitempty"`
		LockTimeoutMs   int64           `json:"lockTimeoutMs,omitempty"`
		WriteID         string          `json:"writeId,omitempty"`
	}

	// Wire payload that a NEW CLI 1.6.0 would emit when the operator runs
	// `browzer workflow patch --arg id=ABC --jq '.featureId = $id'`.
	wirePayload := []byte(`{
	  "verb": "patch",
	  "path": "/tmp/wf.json",
	  "jqExpr": ".featureId = $id",
	  "jqVars": {"id": "ABC"},
	  "writeId": "wf-test-1"
	}`)

	var op oldParams
	if err := json.Unmarshal(wirePayload, &op); err != nil {
		t.Fatalf("OLD daemon decoder MUST tolerate unknown jqVars field; got: %v", err)
	}

	// Surviving fields must be intact.
	if op.Verb != "patch" {
		t.Errorf("verb dropped or mangled: got %q", op.Verb)
	}
	if op.JQExpr != ".featureId = $id" {
		t.Errorf("jqExpr dropped or mangled: got %q", op.JQExpr)
	}
	if op.WriteID != "wf-test-1" {
		t.Errorf("writeId dropped or mangled: got %q", op.WriteID)
	}

	// Confirm the modern decoder also accepts the SAME payload AND populates
	// JQVars correctly — this is the symmetric guarantee for a 1.6.0 daemon.
	var np WorkflowMutateParams
	if err := json.Unmarshal(wirePayload, &np); err != nil {
		t.Fatalf("NEW daemon decoder must accept wire payload; got: %v", err)
	}
	if np.JQVars["id"] != "ABC" {
		t.Errorf("NEW decoder lost jqVars binding; got %v", np.JQVars)
	}
}

// TestWorkflowMutateParams_AbsentJQVarsDecodesAsNilMap (RT-1 partial) —
// when an OLDER CLI sends a payload WITHOUT jqVars to a NEW daemon, the
// daemon must decode it as a nil map (not error) and route through the
// standard ApplyJQ path. Pairs with the additivity test above; together
// they pin both directions of the version-skew matrix.
func TestWorkflowMutateParams_AbsentJQVarsDecodesAsNilMap(t *testing.T) {
	wirePayload := []byte(`{
	  "verb": "patch",
	  "path": "/tmp/wf.json",
	  "jqExpr": ".featureId = \"abc\""
	}`)

	var p WorkflowMutateParams
	if err := json.Unmarshal(wirePayload, &p); err != nil {
		t.Fatalf("absent jqVars must decode without error; got: %v", err)
	}
	if p.JQVars != nil {
		t.Errorf("absent jqVars should decode as nil map, got: %v", p.JQVars)
	}
	// JQExpr must still be intact for the no-vars path.
	if p.JQExpr != `.featureId = "abc"` {
		t.Errorf("jqExpr lost on no-vars path: got %q", p.JQExpr)
	}
}
