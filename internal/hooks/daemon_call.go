package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/daemon"
)

// daemonCallTimeout is the per-RPC ceiling enforced by DaemonCall.
// Matches the 1500 ms used by the retired Node guards in
// packages/skills/hooks/guards/_util.mjs — keeps wall-clock cost
// bounded when the daemon is unresponsive.
const daemonCallTimeout = 1500 * time.Millisecond

// commandRunner abstracts spawning the detached `browzer daemon start
// --background` child so EnsureDaemon can be exercised without forking
// a real process. The default `realCommandRunner` calls exec.Command;
// tests inject a fake to assert the args + SysProcAttr.
type commandRunner interface {
	Start(name string, args ...string) error
}

// realCommandRunner is the production implementation of commandRunner.
// It builds an exec.Cmd, applies the detach-friendly SysProcAttr (so
// the child outlives this process — the Go analog of Node's
// child.unref()), starts it, and intentionally does NOT call Wait.
type realCommandRunner struct{}

// Start spawns the named command with detached process-group semantics
// (Setpgid=true on Unix; no-op on Windows via build-tagged helper).
// Stdio is detached so the parent's pipes are not held open by the
// long-running daemon child.
func (realCommandRunner) Start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	ApplyDetachSysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Release disclaims reaping responsibility; on Windows it is a no-op.
	_ = cmd.Process.Release()
	return nil
}

// runner holds the active commandRunner. Exposed as a variable rather
// than a const so tests can swap it via WithCommandRunner.
var runner commandRunner = realCommandRunner{}

// ensureDaemonOnce is the intra-process guard for EnsureDaemon. Once set
// to true — either after a successful spawn attempt or after a permanent
// exec failure — subsequent calls within the same process skip the fork.
// A new CLI process starts with the zero value (false), so each process
// makes at most one spawn attempt. Cross-process serialisation is handled
// separately via AcquireLock("daemon-spawn") inside EnsureDaemon.
var ensureDaemonOnce atomic.Bool

// WithCommandRunner replaces the package-level commandRunner for the
// duration of the returned restore function. Test-only seam — never
// call from production code.
func WithCommandRunner(r commandRunner) (restore func()) {
	prev := runner
	runner = r
	ensureDaemonOnce.Store(false)
	return func() {
		runner = prev
		ensureDaemonOnce.Store(false)
	}
}

// ResetEnsureDaemonGuard re-arms the atomic.Bool guard (ensureDaemonOnce)
// that gates EnsureDaemon. Test-only seam: a single test process may
// exercise multiple fallback cycles, each of which legitimately wants its
// own spawn attempt.
func ResetEnsureDaemonGuard() {
	ensureDaemonOnce.Store(false)
}

// DaemonCall invokes a JSON-RPC method against the running Browzer
// daemon and returns the raw `result` payload. Semantics mirror the
// retired Node helper:
//
//   - Each call gets a fresh 1.5 s deadline via context.WithTimeout.
//   - On any error (dial, write, decode, RPC error, deadline) the
//     payload {method, params} is appended to
//     $HOME/.browzer/pending-events.jsonl via AppendPendingEvent and a
//     detached respawn of the daemon is attempted via EnsureDaemon.
//   - Errors from AppendPendingEvent / EnsureDaemon are intentionally
//     swallowed — the caller already failed; secondary failures should
//     not mask the primary error returned to the caller.
//
// `params` MUST be JSON-encodable; non-marshalable values will surface
// as the primary error without enqueuing a pending event (the payload
// is unrecoverable anyway).
func DaemonCall(method string, params any) (json.RawMessage, error) {
	if method == "" {
		return nil, errors.New("daemon call: method required")
	}
	// Probe marshal-ability up front so a structurally-bad payload
	// doesn't end up in the pending-events sink as garbage.
	if _, err := json.Marshal(params); err != nil {
		return nil, fmt.Errorf("daemon call: marshal params: %w", err)
	}

	sock := config.SocketPath(os.Getuid())
	client := daemon.NewClient(sock)

	ctx, cancel := context.WithTimeout(context.Background(), daemonCallTimeout)
	defer cancel()

	result, callErr := invokeDaemon(ctx, client, method, params)
	if callErr == nil {
		return result, nil
	}

	// Fallback path: queue the event for later replay + try to bring
	// the daemon back up. Both helpers are best-effort.
	enqueueErr := AppendPendingEvent(pendingEnvelope(method, params))
	respawnErr := EnsureDaemon()
	if enqueueErr != nil || respawnErr != nil {
		// Surface secondary failures as a wrapped error so operators
		// see them in -vv logs, but keep the primary daemon error as
		// the root cause.
		return nil, fmt.Errorf("daemon call: %w (enqueue=%v, respawn=%v)",
			callErr, enqueueErr, respawnErr)
	}
	return nil, callErr
}

// invokeDaemon performs the actual JSON-RPC round-trip against the
// daemon's `daemon.Client`. Wrapped to keep DaemonCall's control flow
// readable. The daemon client exposes typed methods for the well-known
// verbs (Track, Read, SessionRegister, Shutdown, Health); for
// forward-compatibility with new methods we reach into the unexported
// `call` indirectly by selecting the typed path that matches `method`.
//
// Track and SessionRegister are the verbs used by hook subcommands.
// Other method names route through a generic fallback that returns an
// error so callers surface unknown methods explicitly.
func invokeDaemon(ctx context.Context, c *daemon.Client, method string, params any) (json.RawMessage, error) {
	switch method {
	case "Track":
		tp, ok := coerceTrackParams(params)
		if !ok {
			return nil, fmt.Errorf("daemon call Track: params type %T not coercible to daemon.TrackParams", params)
		}
		if err := c.Track(ctx, tp); err != nil {
			return nil, err
		}
		// Track returns an opaque ack; tests treat any non-nil RawMessage
		// as success. Emit a stable "{}" sentinel so callers can assert.
		return json.RawMessage(`{}`), nil
	case "SessionRegister":
		srp, ok := coerceSessionRegisterParams(params)
		if !ok {
			return nil, fmt.Errorf("daemon call SessionRegister: params type %T not coercible to daemon.SessionRegisterParams", params)
		}
		result, err := c.SessionRegister(ctx, srp)
		if err != nil {
			return nil, err
		}
		out, merr := json.Marshal(result)
		if merr != nil {
			return nil, fmt.Errorf("daemon call SessionRegister: marshal result: %w", merr)
		}
		return json.RawMessage(out), nil
	default:
		return nil, fmt.Errorf("daemon call: method %q not wired into hooks client (extend invokeDaemon)", method)
	}
}

// coerceSessionRegisterParams accepts either a daemon.SessionRegisterParams
// value or any map/struct that JSON-round-trips into one. Mirrors the
// loose-typing tolerance in coerceTrackParams.
func coerceSessionRegisterParams(params any) (daemon.SessionRegisterParams, bool) {
	if srp, ok := params.(daemon.SessionRegisterParams); ok {
		return srp, true
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return daemon.SessionRegisterParams{}, false
	}
	var out daemon.SessionRegisterParams
	if err := json.Unmarshal(raw, &out); err != nil {
		return daemon.SessionRegisterParams{}, false
	}
	return out, true
}

// coerceTrackParams accepts either a daemon.TrackParams value or any
// map/struct that JSON-round-trips into one. The Node guards passed
// loosely-typed maps; preserving that flexibility lets hook
// subcommands assemble payloads with map[string]any without importing
// the daemon package directly.
func coerceTrackParams(params any) (daemon.TrackParams, bool) {
	if tp, ok := params.(daemon.TrackParams); ok {
		return tp, true
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return daemon.TrackParams{}, false
	}
	var out daemon.TrackParams
	if err := json.Unmarshal(raw, &out); err != nil {
		return daemon.TrackParams{}, false
	}
	return out, true
}

// pendingEnvelope is the on-disk shape of a queued event — preserves
// both method and params so a future replay job can re-issue the call,
// plus the enqueue timestamp for diagnostics. Returns map[string]any
// so callers do not need to import an internal struct shape; the JSON
// encoding is deterministic key-sorted by encoding/json.
//
// Kept small (single JSON object) to stay under PIPE_BUF (4096 bytes)
// for O_APPEND atomicity (see pending_events.go's
// pendingEventsMaxLineBytes constant).
func pendingEnvelope(method string, params any) map[string]any {
	return map[string]any{
		"method":   method,
		"params":   params,
		"enqueued": time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// EnsureDaemon spawns `browzer daemon start --background` in a detached
// process group when one has not already been spawned by this process.
//
// Two layers of protection prevent stampedes:
//
//  1. Intra-process: ensureDaemonOnce (atomic.Bool) is a cheap early-exit
//     for the common case where this process already attempted a spawn.
//     On exec failure the guard is left pinned (true) so a missing binary
//     does not trigger repeated fork+exec attempts across subsequent hook
//     calls in the same process.
//
//  2. Cross-process: AcquireLock("daemon-spawn") serialises concurrent CLI
//     processes (e.g. ten parallel subagent dispatches each starting after
//     a daemon crash). The first process to acquire the lock proceeds with
//     the fork; the rest see ok=false and return nil immediately — they let
//     the lock-holder do the spawn and will succeed on their next DaemonCall
//     dial attempt.
//
// On Unix the child is placed in its own process group via
// SysProcAttr.Setpgid=true and stdio is fully detached. No Wait() is
// invoked — the child outlives this CLI invocation.
//
// Errors surface only when exec.Start itself fails (e.g. the `browzer`
// binary is not on PATH). Spawn-fast / spawn-slow timing is not the
// caller's concern — by the time the next DaemonCall retries, either
// the daemon is up or the call falls back again. The hook surface is
// designed to tolerate either outcome.
func EnsureDaemon() error {
	// Intra-process hot-path: if this process already attempted a spawn
	// (successfully or with a permanent exec failure), skip the lock probe.
	if !ensureDaemonOnce.CompareAndSwap(false, true) {
		return nil
	}

	// Cross-process serialisation: try to acquire the advisory file lock.
	// AcquireLock is non-blocking — if another process holds it, ok=false.
	release, ok, lockErr := AcquireLock("daemon-spawn")
	if lockErr != nil {
		// I/O error acquiring the lock. Fall back to attempting the spawn
		// without cross-process coordination (graceful degradation — better
		// than silently eating the error and not spawning at all).
		// Leave ensureDaemonOnce=true so this process does not retry.
	} else if !ok {
		// Another process is already spawning the daemon. Leave our intra-
		// process guard set so we do not retry, and return nil — the lock-
		// holder's spawn will bring the daemon up.
		return nil
	}

	spawnErr := runner.Start("browzer", "daemon", "start", "--background")

	// Release the cross-process lock now that we've issued Start.
	// A nil release (lockErr != nil path above) is guarded.
	if release != nil {
		_ = release()
	}

	if spawnErr != nil {
		// Leave ensureDaemonOnce=true (do NOT reset). A permanent failure
		// (e.g. binary not on PATH) must not cause the same process to
		// retry the fork on every subsequent hook call. Transient OS errors
		// (EAGAIN) are rare and the cost of a single miss is negligible.
		return fmt.Errorf("respawn daemon: %w", spawnErr)
	}
	return nil
}
