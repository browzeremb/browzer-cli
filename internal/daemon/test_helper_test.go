package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// EphemeralDaemon is a daemon spun up for the duration of one test.
// The Server runs in-process on a per-test Unix socket under t.TempDir().
// t.Cleanup is registered so the socket is removed and the server stopped
// when the test ends.
type EphemeralDaemon struct {
	Client     *Client
	Server     *Server
	SocketPath string
	stopFn     func()
}

// Stop tears down the ephemeral daemon. Also called automatically via
// t.Cleanup. Safe to call multiple times.
func (e *EphemeralDaemon) Stop() {
	if e.stopFn != nil {
		e.stopFn()
	}
}

// SpinUpEphemeralDaemon starts a Server on a fresh socket under t.TempDir().
// The returned EphemeralDaemon's Client is ready to drive Read / Track /
// SessionRegister or any other JSON-RPC method served by the daemon.
// Failure modes (socket-bind, dial timeout) call t.Fatal directly — callers
// don't need to check error returns.
func SpinUpEphemeralDaemon(t *testing.T) *EphemeralDaemon {
	t.Helper()
	return spinUpEphemeralDaemonImpl(t)
}

func spinUpEphemeralDaemonImpl(t *testing.T) *EphemeralDaemon {
	t.Helper()
	sockDir, err := os.MkdirTemp("/tmp", "brz-eph-*")
	if err != nil {
		t.Fatalf("SpinUpEphemeralDaemon: MkdirTemp: %v", err)
	}
	sock := filepath.Join(sockDir, "daemon.sock")

	srv := NewServer(Options{
		SocketPath: sock,
	})

	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = srv.Serve(ctx) }()

	// Wait up to 2s for the socket to come alive.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", sock); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Sanity-check that the daemon is reachable before returning.
	cli := NewClient(sock)
	if _, err := cli.Health(t.Context()); err != nil {
		cancel()
		srv.Stop()
		_ = os.RemoveAll(sockDir)
		t.Fatalf("SpinUpEphemeralDaemon: daemon not reachable after 2s: %v", err)
	}

	stop := func() {
		cancel()
		srv.Stop()
		_ = os.RemoveAll(sockDir)
	}
	t.Cleanup(stop)

	return &EphemeralDaemon{
		Client:     cli,
		Server:     srv,
		SocketPath: sock,
		stopFn:     stop,
	}
}
