package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

	ctx, cancel := context.WithCancel(context.Background())
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
