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

// SpinUpEphemeralDaemon starts a Server on a fresh socket under t.TempDir()
// with a 30-minute keepalive (so the idle drainer is inert during a test).
// The returned EphemeralDaemon's Client is ready to drive WorkflowMutate or
// any other JSON-RPC method. Failure modes (socket-bind, dial timeout) call
// t.Fatal directly — callers don't need to check error returns.
//
// By default BROWZER_NO_SCHEMA_CHECK=1 is set so that tests exercising
// daemon mechanics with minimal fixtures do not hit CUE validation. Tests
// that specifically want to exercise schema validation should call
// SpinUpEphemeralDaemonWithValidation (does NOT set the bypass) instead
// of trying to clear the env var after the fact — t.Setenv is restored
// at test end, but writes to it after this helper has returned mean the
// daemon goroutine may have already cached the bypass decision.
//
// QA-005 (2026-05-04): the bypass was originally documented as "callers
// can clear it" but in practice the bypass is consulted at apply time,
// not daemon-init time, so clearing it later does work — however the
// dedicated WithValidation variant makes the intent unambiguous and
// keeps the bypass coverage scoped.
func SpinUpEphemeralDaemon(t *testing.T) *EphemeralDaemon {
	t.Helper()
	// Bypass schema validation by default — these tests exercise daemon
	// mechanics (queue ordering, crash recovery, version handshake), not
	// schema enforcement (the CUE validator has dedicated tests).
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	return spinUpEphemeralDaemonImpl(t)
}

// SpinUpEphemeralDaemonWithValidation is the variant that does NOT set
// BROWZER_NO_SCHEMA_CHECK. Use this when the test specifically exercises
// CUE validation through the daemon path (e.g. asserting that the daemon
// rejects a malformed payload).
//
// QA-005 (2026-05-04): introduced so schema-enforcement tests don't have
// to chase down the default env-var bypass.
func SpinUpEphemeralDaemonWithValidation(t *testing.T) *EphemeralDaemon {
	t.Helper()
	// Explicitly clear the env var in case the parent test set it.
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "")
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
		SocketPath:        sock,
		WorkflowKeepalive: 30 * time.Minute,
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
