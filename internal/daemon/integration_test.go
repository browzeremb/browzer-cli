package daemon

import (
	"context"
	"os"
	"path/filepath"
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
