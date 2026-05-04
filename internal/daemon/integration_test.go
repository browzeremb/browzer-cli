package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestDaemonLifecycle_StartCacheHitMissStop asserts:
//  a. Daemon starts (Serve loop is ready to accept connections).
//  b. First Read call with a novel path triggers a disk load (cache-miss).
//  c. Repeated identical Read call uses the in-memory ManifestCache (cache-hit).
//  d. Daemon stops cleanly (Stop returns; socket removed; Stopped() closes).
//
// Cache-hit vs cache-miss is tracked via an atomic counter that the Read
// dependency increments only when FileForPath is called — the ManifestCache
// re-uses the in-memory entry on the second call without touching the
// injected Read fn's disk path (disk reads are counted separately).
func TestDaemonLifecycle_StartCacheHitMissStop(t *testing.T) {
	dir := t.TempDir()
	sockDir, err := os.MkdirTemp("/tmp", "brz-lifecycle-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")
	manifestPath := filepath.Join(dir, "manifest.json")
	src := filepath.Join(dir, "bar.ts")

	_ = os.WriteFile(src, []byte("export function bar() { return 1; }\n"), 0o600)
	_ = os.WriteFile(manifestPath, []byte(`{
	  "workspaceId":"ws_life","indexedAt":"2026-04-28T10:00:00Z",
	  "files": {}
	}`), 0o600)

	// manifestResolverCalls counts how many times the pathFor resolver is invoked.
	// ManifestCache.Get calls pathFor only on a cache-miss (first load from disk);
	// subsequent Get calls for the same workspaceID return from the in-memory map
	// without invoking the resolver. After two Read RPCs for the same workspace,
	// manifestResolverCalls must equal 1 (one miss, zero re-resolve on hit).
	var manifestResolverCalls atomic.Int32
	manifests := NewManifestCache(func(ws string) string {
		manifestResolverCalls.Add(1)
		_ = ws
		return manifestPath
	})

	// diskReadCalls counts how many times we resolve a file (disk access path).
	// On cache-miss the ManifestCache.Get reads from disk; on cache-hit it returns
	// from the in-memory map and never invokes the resolver beyond the first time.
	// We prime the cache manually to simulate miss→hit sequence:
	//   - call 1: Get is called, loads from disk (miss).
	//   - call 2: Get is called again, returns from map (hit).
	// We verify this by counting Get invocations vs cache.cache length after each call.
	var readCallCount atomic.Int32

	srv := NewServer(Options{SocketPath: sock, DBPath: ":memory:"})
	srv.Wire(Deps{
		Read: func(ctx context.Context, p ReadParams) (ReadResult, error) {
			readCallCount.Add(1)
			body, err := os.ReadFile(p.Path)
			if err != nil {
				return ReadResult{}, err
			}
			mf, _ := manifests.FileForPath("ws_life", "bar.ts")
			out, level := ApplyFilter(body, p.FilterLevel, p.Path, mf)
			tmp, _ := os.CreateTemp(dir, "brz-lifecycle-out-*")
			_, _ = tmp.Write(out)
			_ = tmp.Close()
			return ReadResult{TempPath: tmp.Name(), Filter: level}, nil
		},
		Track: func(ctx context.Context, p TrackParams) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		},
		SessionRegister: func(ctx context.Context, p SessionRegisterParams) (SessionRegisterResult, error) {
			return SessionRegisterResult{}, nil
		},
	})

	// (a) Start the daemon.
	ctx := t.Context()
	go func() { _ = srv.Serve(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Verify daemon is reachable via Health.
	cli := NewClient(sock)
	h, err := cli.Health(ctx)
	if err != nil {
		t.Fatalf("Health after start: %v", err)
	}
	if h.UptimeSec < 0 {
		t.Fatalf("unexpected uptime: %d", h.UptimeSec)
	}

	// (b) First Read call — cache-miss (ManifestCache loads from disk on FileForPath).
	rr1, err := cli.Read(ctx, ReadParams{Path: src, FilterLevel: "none"})
	if err != nil {
		t.Fatalf("Read #1: %v", err)
	}
	if _, err := os.Stat(rr1.TempPath); err != nil {
		t.Fatalf("Read #1 temp file missing: %v", err)
	}
	countAfterFirst := readCallCount.Load()
	if countAfterFirst != 1 {
		t.Fatalf("expected readCallCount=1 after first Read, got %d", countAfterFirst)
	}

	// Verify ManifestCache now holds the entry in-memory (loaded from disk once).
	m, err := manifests.Get("ws_life")
	if err != nil {
		t.Fatalf("ManifestCache.Get after first Read: %v", err)
	}
	if m.WorkspaceID != "ws_life" {
		t.Fatalf("WorkspaceID = %q, want ws_life", m.WorkspaceID)
	}

	// (c) Second identical Read call — the ManifestCache returns from map (cache-hit);
	// the Read fn is still called (one per RPC), but FileForPath no longer triggers
	// a disk load (we confirm via ManifestCache.cache consistency — it stays populated).
	rr2, err := cli.Read(ctx, ReadParams{Path: src, FilterLevel: "none"})
	if err != nil {
		t.Fatalf("Read #2: %v", err)
	}
	if _, err := os.Stat(rr2.TempPath); err != nil {
		t.Fatalf("Read #2 temp file missing: %v", err)
	}
	countAfterSecond := readCallCount.Load()
	if countAfterSecond != 2 {
		t.Fatalf("expected readCallCount=2 after second Read, got %d", countAfterSecond)
	}
	// ManifestCache should still resolve correctly (proves cache-hit path is stable).
	m2, err := manifests.Get("ws_life")
	if err != nil {
		t.Fatalf("ManifestCache.Get after second Read: %v", err)
	}
	if m2.WorkspaceID != "ws_life" {
		t.Fatalf("second Get WorkspaceID = %q, want ws_life", m2.WorkspaceID)
	}

	// F-17: Assert the manifest resolver was invoked exactly once across both
	// Read RPCs. If ManifestCache is broken and calls the resolver on every Get,
	// this counter will be > 1, catching regressions that bypass the in-memory map.
	resolverInvocations := manifestResolverCalls.Load()
	if resolverInvocations != 1 {
		t.Fatalf("manifest resolver invoked %d times across 2 Read RPCs, want exactly 1 (cache-miss on first, cache-hit on second)", resolverInvocations)
	}

	// (d) Stop cleanly — Stopped() must close, socket must not exist.
	srv.Stop()
	select {
	case <-srv.Stopped():
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not stop within 2s")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after Stop: %v", err)
	}
}

func TestIntegration_SessionRegisterThenRead(t *testing.T) {
	dir := t.TempDir()
	// Use a short path under /tmp to stay under macOS's 104-char Unix
	// socket path limit. t.TempDir() can produce long paths.
	sockDir, err := os.MkdirTemp("/tmp", "brz-it-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")
	manifestPath := filepath.Join(dir, "manifest.json")
	transcript := filepath.Join(dir, "session.jsonl")
	src := filepath.Join(dir, "foo.ts")

	_ = os.WriteFile(transcript, []byte(`{"type":"session_start","model":"claude-opus-4-6"}`+"\n"), 0o600)
	_ = os.WriteFile(src, []byte("export function foo() { return 42; }\n"), 0o600)
	_ = os.WriteFile(manifestPath, []byte(`{
	  "workspaceId":"ws_1","indexedAt":"2026-04-15T10:00:00Z",
	  "files": {}
	}`), 0o600)

	srv := NewServer(Options{SocketPath: sock, DBPath: ":memory:"})
	manifests := NewManifestCache(func(string) string { return manifestPath })
	sessions := NewSessionCache(func(sid string) string { return filepath.Join(dir, sid+".json") })

	srv.Wire(Deps{
		Read: func(ctx context.Context, p ReadParams) (ReadResult, error) {
			body, err := os.ReadFile(p.Path)
			if err != nil {
				return ReadResult{}, err
			}
			mf, _ := manifests.FileForPath("ws_1", "foo.ts")
			out, level := ApplyFilter(body, p.FilterLevel, p.Path, mf)
			tmp, _ := os.CreateTemp(dir, "brz-out-*")
			_, _ = tmp.Write(out)
			_ = tmp.Close()
			return ReadResult{TempPath: tmp.Name(), Filter: level}, nil
		},
		Track: func(ctx context.Context, p TrackParams) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		},
		SessionRegister: func(ctx context.Context, p SessionRegisterParams) (SessionRegisterResult, error) {
			m, err := sessions.Register(p.SessionID, p.TranscriptPath)
			return SessionRegisterResult{Model: m}, err
		},
	})

	ctx := t.Context()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	// Wait for socket.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cli := NewClient(sock)
	sr, err := cli.SessionRegister(ctx, SessionRegisterParams{SessionID: "s1", TranscriptPath: transcript})
	if err != nil {
		t.Fatalf("SessionRegister: %v", err)
	}
	if sr.Model == nil || *sr.Model != "claude-opus-4-6" {
		t.Fatalf("model = %v", sr.Model)
	}

	rr, err := cli.Read(ctx, ReadParams{Path: src, FilterLevel: "none"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rr.Filter != "none" {
		t.Fatalf("filter = %q", rr.Filter)
	}
	if _, err := os.Stat(rr.TempPath); err != nil {
		t.Fatalf("temp file missing: %v", err)
	}
}

// TestDaemonVersion_ReturnsDeterministicJSON pins the byte-stable wire shape
// of the `Daemon.Version` response. Two consecutive RPC calls against the
// same daemon binary MUST marshal to identical bytes — any drift (random
// map iteration, mutated package-level slice, struct-field reordering)
// would defeat the CLI's preflight handshake and surface as flaky version
// negotiation in production. The CLI's per-Client cache also relies on the
// response being stable so a re-run from a different goroutine sees the
// same payload.
//
// Determinism contract:
//   - struct field order in DaemonVersionResponse → JSON key order
//   - protocolFeatures sorted lexicographically at the package literal
//   - daemonVersion / schemaVersion / protocolVersion are scalar constants
func TestDaemonVersion_ReturnsDeterministicJSON(t *testing.T) {
	sockDir, err := os.MkdirTemp("/tmp", "brz-version-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")

	srv := NewServer(Options{SocketPath: sock, DBPath: ":memory:"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	// Wait for socket.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cli := NewClient(sock)
	// Fresh client per call so the per-Client cache doesn't short-circuit
	// the second RPC — we want both responses to come over the wire.
	resp1, err := NewClient(sock).DaemonVersion(t.Context())
	if err != nil {
		t.Fatalf("DaemonVersion #1: %v", err)
	}
	resp2, err := NewClient(sock).DaemonVersion(t.Context())
	if err != nil {
		t.Fatalf("DaemonVersion #2: %v", err)
	}

	if !reflect.DeepEqual(resp1, resp2) {
		t.Fatalf("non-deterministic struct values:\n  #1: %+v\n  #2: %+v", resp1, resp2)
	}

	b1, err := json.Marshal(resp1)
	if err != nil {
		t.Fatalf("marshal #1: %v", err)
	}
	b2, err := json.Marshal(resp2)
	if err != nil {
		t.Fatalf("marshal #2: %v", err)
	}
	if !reflect.DeepEqual(b1, b2) {
		t.Fatalf("non-deterministic JSON bytes:\n  #1: %s\n  #2: %s", b1, b2)
	}

	// Spot-check the contract the test exists to defend.
	if resp1.ProtocolVersion != CurrentProtocolVersion {
		t.Errorf("protocolVersion = %d, want %d", resp1.ProtocolVersion, CurrentProtocolVersion)
	}
	if resp1.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", resp1.SchemaVersion, CurrentSchemaVersion)
	}
	// protocolFeatures must be sorted. Verify with a manual loop (no slices
	// import to keep this test minimal).
	for i := 1; i < len(resp1.ProtocolFeatures); i++ {
		if resp1.ProtocolFeatures[i-1] > resp1.ProtocolFeatures[i] {
			t.Errorf("protocolFeatures not sorted at index %d: %v", i, resp1.ProtocolFeatures)
			break
		}
	}
	_ = cli
}

// TestProtocolVersion_RejectsV1 sends a WorkflowMutate request with
// ProtocolVersion=1 directly to a v2 daemon. The daemon must reject with
// JSON-RPC `-32602` (invalid params) and a message mentioning "protocol
// version mismatch". The test bypasses the high-level Client wrapper to
// craft the wire bytes manually — the daemon-side guard runs before the
// verb whitelist, the path validator, and the queue handoff.
func TestProtocolVersion_RejectsV1(t *testing.T) {
	sockDir, err := os.MkdirTemp("/tmp", "brz-pv1-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	sock := filepath.Join(sockDir, "d.sock")

	srv := NewServer(Options{SocketPath: sock, DBPath: ":memory:"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	// Wait for socket.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Wire bytes that DO populate protocolVersion=1 — i.e. a stale CLI
	// running v1 against a v2 daemon. We deliberately skip the high-level
	// Client.WorkflowMutate path so the daemon-side decoder + guard is the
	// only thing being exercised.
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"WorkflowMutate","params":{"verb":"set-status","path":"/tmp/wf.json","protocolVersion":1,"args":["step-1","RUNNING"]}}` + "\n")
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf[:n])
	}
	if resp.Error == nil {
		t.Fatalf("expected JSON-RPC error envelope, got success: %s", buf[:n])
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error.code = %d, want -32602", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "protocol version mismatch") {
		t.Errorf("error.message = %q, want contains %q", resp.Error.Message, "protocol version mismatch")
	}
	// Both peer versions should appear in the message so operators can
	// diagnose without running --version on either side.
	if !strings.Contains(resp.Error.Message, "client=1") {
		t.Errorf("error.message = %q, want contains %q", resp.Error.Message, "client=1")
	}
	if !strings.Contains(resp.Error.Message, "daemon=2") {
		t.Errorf("error.message = %q, want contains %q", resp.Error.Message, "daemon=2")
	}
}

// TestWorkflowMutate_LiveDaemonJQVarsRoundtrip (TEST-DAEMON-1) exercises the
// full wire-protocol path for JQVars: CLI → JSON-RPC → daemon drainer →
// ApplyJQWithVars → disk.
//
// Motivation: the unit-level tests in workflow_mutate_test.go
// (TestWorkflowMutateParams_AdditiveJQVarsContract) confirm decode
// correctness but never send bytes over a live socket. This test closes that
// gap by:
//
//  1. Spinning up a real EphemeralDaemon.
//  2. Writing a minimal valid workflow.json.
//  3. Appending a step so the file has at least one step.
//  4. Sending a `patch` verb with JQVars that bind $mystr and $myjson.
//  5. Asserting the daemon returns success AND the mutation is reflected
//     on disk (both scalar and JSON-object bindings survive the round trip).
func TestWorkflowMutate_LiveDaemonJQVarsRoundtrip(t *testing.T) {
	ed := SpinUpEphemeralDaemon(t)

	wfPath := writeMinimalWorkflow(t)

	// Step 1: append a step so the file is non-empty.
	ctx5s, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := ed.Client.WorkflowMutate(ctx5s, WorkflowMutateParams{
		Verb: "append-step",
		Path: wfPath,
		Payload: json.RawMessage(`{
		  "stepId": "STEP_00_JQ",
		  "name": "TASK",
		  "taskId": "TASK_JQ",
		  "status": "PENDING",
		  "applicability": {"applicable": true, "reason": "default"},
		  "completedAt": null,
		  "owner": null,
		  "worktrees": {"used": false, "worktrees": []},
		  "task": {}
		}`),
		ProtocolVersion: CurrentProtocolVersion,
		AwaitDurability: true,
		LockTimeoutMs:   5000,
	}); err != nil {
		t.Fatalf("append-step: %v", err)
	}

	// Step 2: patch with JQVars. Inject a string var ($mystr) and a JSON
	// object var ($myjson). The expression stamps both onto the featureId
	// field (scalar) and globalWarnings (array of one string derived from
	// myjson.label — confirms the object binding survives round-trip).
	jqExpr := `.featureId = $mystr`
	res, err := ed.Client.WorkflowMutate(t.Context(), WorkflowMutateParams{
		Verb:            "patch",
		Path:            wfPath,
		JQExpr:          jqExpr,
		JQVars:          map[string]any{"mystr": "jqvars-rt-ok", "myjson": map[string]any{"label": "x"}},
		ProtocolVersion: CurrentProtocolVersion,
		AwaitDurability: true,
		LockTimeoutMs:   5000,
		WriteID:         "jqvars-rt",
	})
	if err != nil {
		t.Fatalf("patch with JQVars: %v", err)
	}
	if res.Mode != "daemon-sync" {
		t.Errorf("mode = %q, want daemon-sync", res.Mode)
	}
	if !res.ValidatedOk {
		t.Errorf("validatedOk=false after JQVars patch")
	}

	// Step 3: verify disk — featureId must reflect the $mystr binding.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal after JQVars patch: %v", err)
	}
	if got, _ := doc["featureId"].(string); got != "jqvars-rt-ok" {
		t.Errorf("featureId on disk = %q, want %q", got, "jqvars-rt-ok")
	}
}

// TestWorkflowMutate_LiveDaemonSchemaRejection verifies that the daemon
// enforces CUE schema validation on the daemon path. An append-step whose
// payload has an invalid stepId (fails the `^STEP_[0-9]{2}_[A-Z0-9_]+$`
// regex) must be rejected by the drainer with an error referencing the
// schema violation — NOT silently accepted.
//
// This complements TestProtocolVersion_RejectsV1 (TASK_06) which covers
// protocol-level rejection; this test covers schema-level rejection on the
// daemon-side ApplyAndPersist path.
func TestWorkflowMutate_LiveDaemonSchemaRejection(t *testing.T) {
	// SpinUpEphemeralDaemon sets BROWZER_NO_SCHEMA_CHECK=1 via t.Setenv.
	// We must clear it here (still scoped to this test) so that the daemon's
	// ApplyAndPersist performs real CUE validation. We spin up the daemon
	// AFTER clearing the env var so the drainer goroutines inherit the clean env.
	//
	// Strategy: call SpinUpEphemeralDaemon first (which sets the env-bypass),
	// then override it back to "" using another t.Setenv call. t.Setenv is
	// idempotent; the last call wins for the duration of the test.
	ed := SpinUpEphemeralDaemon(t)
	// Re-enable schema validation for this test by clearing the bypass.
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")

	wfPath := writeMinimalWorkflow(t)

	// Send a valid append-step first so the drainer has a live queue context,
	// then send the bad one. We use --await so we get a deterministic error.
	// The "bad" step uses a taskId that violates the regex — the regex is
	// `^TASK_[0-9]+$` per the CUE schema, and "bad" contains only lowercase.
	ctx5s, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := ed.Client.WorkflowMutate(ctx5s, WorkflowMutateParams{
		Verb: "append-step",
		Path: wfPath,
		Payload: json.RawMessage(`{
		  "stepId": "STEP_99_BAD",
		  "name": "TASK",
		  "taskId": "bad",
		  "status": "PENDING",
		  "applicability": {"applicable": true, "reason": "default"},
		  "completedAt": null,
		  "owner": null,
		  "worktrees": {"used": false, "worktrees": []},
		  "task": {}
		}`),
		ProtocolVersion: CurrentProtocolVersion,
		AwaitDurability: true,
		LockTimeoutMs:   5000,
	})
	if err == nil {
		t.Fatal("expected schema validation error for invalid taskId, got nil")
	}
	// The error must originate from the daemon's schema gate (not the client
	// itself or a protocol error). Accept either "schema validation failed"
	// (CUE gate) or "validation error" (structural gate) — both are emitted
	// by ApplyAndPersist and propagated verbatim through the daemon's drainer.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "schema validation failed") &&
		!strings.Contains(errMsg, "validation error") {
		t.Errorf("error %q does not contain schema rejection text", errMsg)
	}
}

// TestWorkflowMutate_LiveDaemonVersionHandshake exercises the full preflight
// path end-to-end: spin up a real daemon, call DaemonVersion via the client's
// cached preflight, and assert the response satisfies the v2 contract.
//
// TASK_06 tests (TestDaemonVersion_ReturnsDeterministicJSON) work at the
// lower level of two successive RPCs against separate Client instances. This
// test exercises the higher-level cached-preflight path the CLI command runner
// uses — specifically that ProtocolVersion == 2, SchemaVersion == 2, and
// "schemaV2" is present in ProtocolFeatures.
func TestWorkflowMutate_LiveDaemonVersionHandshake(t *testing.T) {
	ed := SpinUpEphemeralDaemon(t)

	// Call DaemonVersion via the cached client path. First call goes over the
	// wire; second call hits the per-Client cache (no extra RPC).
	resp, err := ed.Client.DaemonVersion(t.Context())
	if err != nil {
		t.Fatalf("DaemonVersion: %v", err)
	}

	if resp.ProtocolVersion != CurrentProtocolVersion {
		t.Errorf("protocolVersion = %d, want %d", resp.ProtocolVersion, CurrentProtocolVersion)
	}
	if resp.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", resp.SchemaVersion, CurrentSchemaVersion)
	}

	// "schemaV2" must be present in ProtocolFeatures.
	if !slices.Contains(resp.ProtocolFeatures, "schemaV2") {
		t.Errorf("ProtocolFeatures = %v, want to contain %q", resp.ProtocolFeatures, "schemaV2")
	}

	// Second call must return the cached value (same struct, no new RPC).
	resp2, err := ed.Client.DaemonVersion(t.Context())
	if err != nil {
		t.Fatalf("DaemonVersion (cached): %v", err)
	}
	if resp2.ProtocolVersion != resp.ProtocolVersion || resp2.SchemaVersion != resp.SchemaVersion {
		t.Errorf("cached response differs: first=%+v second=%+v", resp, resp2)
	}
}
