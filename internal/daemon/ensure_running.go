// Package daemon — ensure_running.go
//
// EnsureRunning is the auto-spawn helper used by hooks and CLI verbs that
// want to talk to the daemon "even if it isn't up yet". Mirrors the older
// inline auto-spawn loop in commands/* (50 / 100 / 250 ms backoff) and
// centralises it so the CLI dispatch + read CLI + token-economy hooks
// don't drift.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// EnsureRunningOptions controls EnsureRunning. Zero value is fine for the
// common case: dial → spawn `browzer daemon start --background` if dial
// fails → retry with 50/100/250 ms backoff.
type EnsureRunningOptions struct {
	// SocketPath is the abs path of the daemon's Unix socket. Required.
	SocketPath string
	// SpawnArgs are the args passed to os.StartProcess when the daemon
	// is not up. nil means "no spawn — just probe and report". The
	// historic value is `["daemon", "start", "--background"]`.
	SpawnArgs []string
	// MaxAttempts caps the dial probes. Zero means 4 (initial dial + 3
	// retries — preserves the historic "give up after ~400 ms" behaviour).
	MaxAttempts int
}

// EnsureRunning probes the daemon socket. If unreachable AND SpawnArgs is
// non-nil, it forks `os.Executable() <SpawnArgs...>` and retries with
// 50/100/250 ms backoff. Returns nil when the daemon is reachable, or an
// error if SpawnArgs is empty / spawn failed / all retries exhausted.
//
// Idempotent: a daemon that's already running is detected on the first
// probe and EnsureRunning returns immediately.
func EnsureRunning(ctx context.Context, opts EnsureRunningOptions) error {
	if opts.SocketPath == "" {
		return errors.New("EnsureRunning: SocketPath required")
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 4
	}

	if err := dialProbe(opts.SocketPath); err == nil {
		return nil
	}

	if len(opts.SpawnArgs) == 0 {
		return errors.New("daemon not running and SpawnArgs is empty")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}

	cmd := exec.Command(exe, opts.SpawnArgs...)
	// Detach: parent should not block on the child's stdio.
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	// Best-effort: release the child to its own process group.
	_ = cmd.Process.Release()

	// Backoff: 50 ms, 100 ms, 250 ms. Total ~400 ms before giving up.
	backoffs := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond}
	for i := 0; i < maxAttempts-1; i++ {
		var wait time.Duration
		if i < len(backoffs) {
			wait = backoffs[i]
		} else {
			wait = backoffs[len(backoffs)-1]
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if err := dialProbe(opts.SocketPath); err == nil {
			return nil
		}
	}
	return errors.New("daemon spawn timed out after backoff")
}

// dialProbe is a single dial-and-close — used to test reachability without
// emitting a JSON-RPC call. dialOnce is the helper already used by client.go.
func dialProbe(sockPath string) error {
	conn, err := dialOnce(sockPath)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
