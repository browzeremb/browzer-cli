package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClient_HealthRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	srv := NewServer(Options{SocketPath: sock, DBPath: "/tmp/foo.db"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c := NewClient(sock)
	res, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if res.DBPath != "/tmp/foo.db" {
		t.Fatalf("DBPath = %q", res.DBPath)
	}
}

// startTestServerWithTrack spins a daemon backed by the supplied Track dep
// and returns a connected client. Used by the Client_Track tests below.
//
// Uses a short /tmp/brz-trk-* tempdir for the socket because Unix socket
// paths are capped at 104 bytes on macOS (sun_path), and t.TempDir() under
// /var/folders/<long-hash>/T/<long-test-name>/001 routinely overshoots when
// the test name is long.
func startTestServerWithTrack(
	t *testing.T,
	track func(context.Context, TrackParams) (map[string]any, error),
) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "brz-trk-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	srv := NewServer(Options{SocketPath: sock, DBPath: ":memory:"})
	srv.Wire(Deps{
		Read: func(_ context.Context, _ ReadParams) (ReadResult, error) {
			return ReadResult{}, nil
		},
		Track: track,
		SessionRegister: func(_ context.Context, _ SessionRegisterParams) (SessionRegisterResult, error) {
			return SessionRegisterResult{}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(srv.Stop)
	go func() { _ = srv.Serve(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return NewClient(sock)
}

// TestClient_Track_SuccessAck guards the happy path: server returns
// `{"ok": true}` and the client returns nil.
func TestClient_Track_SuccessAck(t *testing.T) {
	c := startTestServerWithTrack(t, func(_ context.Context, _ TrackParams) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	})
	err := c.Track(context.Background(), TrackParams{Source: "s", Command: "c", SavedTokens: 1})
	if err != nil {
		t.Fatalf("Track returned error on ok:true ack: %v", err)
	}
}

// TestClient_Track_RejectedAckSurfacesError is the regression guard for the
// silent-drop bug: when the server returns `{"ok": false, "error": "..."}`,
// previous Client.Track ignored the inner failure and returned nil, leaving
// the caller unaware that the event was dropped. The fix surfaces it.
func TestClient_Track_RejectedAckSurfacesError(t *testing.T) {
	c := startTestServerWithTrack(t, func(_ context.Context, _ TrackParams) (map[string]any, error) {
		return map[string]any{"ok": false, "error": "disk full"}, nil
	})
	err := c.Track(context.Background(), TrackParams{Source: "s", Command: "c", SavedTokens: 1})
	if err == nil {
		t.Fatal("Track returned nil despite server-side ok:false ack")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("error did not surface server-side message; got: %v", err)
	}
}

// TestClient_Track_RejectedAckWithoutMessage covers the edge case where the
// server returned ok:false but no "error" key — the client must still
// produce a non-nil error.
func TestClient_Track_RejectedAckWithoutMessage(t *testing.T) {
	c := startTestServerWithTrack(t, func(_ context.Context, _ TrackParams) (map[string]any, error) {
		return map[string]any{"ok": false}, nil
	})
	err := c.Track(context.Background(), TrackParams{Source: "s", Command: "c", SavedTokens: 1})
	if err == nil {
		t.Fatal("Track returned nil despite ok:false ack (no error key)")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("error message did not mention refusal; got: %v", err)
	}
}
