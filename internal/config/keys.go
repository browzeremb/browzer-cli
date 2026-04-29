package config

// Single source of truth for every persistent path, env var, and config key
// shared by the Browzer CLI binary and the Browzer daemon.
//
// The Node hooks (packages/skills/hooks/guards/*.mjs) do NOT import from
// here — they call the daemon over its Unix socket, which encapsulates
// these constants.
//
// Spec: docs/CHANGELOG.md §2026-04-15 "CLI token economy" (original spec §7.3 archived in git history).

import (
	"fmt"
	"os"
	"path/filepath"
)

// --- File paths ---------------------------------------------------------

// HistoryDBPath returns the SQLite tracker path. Honors XDG_DATA_HOME
// when set; falls back to ~/.browzer/history.db otherwise.
func HistoryDBPath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "browzer", "history.db")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer", "history.db")
}

// ConfigPath returns the user-config path (~/.browzer/config.json).
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer", "config.json")
}

// PIDPath returns the daemon PID file path.
func PIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer", "daemon.pid")
}

// SocketPath returns the per-uid Unix socket path used by the daemon.
// The uid suffix isolates concurrent users on the same machine.
//
// Honors the BROWZER_DAEMON_SOCKET env var when set; this is the test-time
// override used by the in-process daemon harness in
// `internal/daemon/workflow_mutate_test.go` and the integration tests in
// `internal/commands/`.
func SocketPath(uid int) string {
	if env := os.Getenv(EnvDaemonSocket); env != "" {
		return env
	}
	return fmt.Sprintf("/tmp/browzer-daemon.%d.sock", uid)
}

// SessionCachePath returns the per-session cache file (model lookup).
func SessionCachePath(sessionID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer", "sessions", sessionID+".json")
}

// ManifestCachePath returns the local copy of a workspace manifest.
func ManifestCachePath(workspaceID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer", "workspaces", workspaceID, "manifest.json")
}

// TelemetryStatePath returns the file recording the last successful
// telemetry flush timestamp.
func TelemetryStatePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browzer", "telemetry.state")
}

// --- Config keys (persisted in ~/.browzer/config.json) ------------------

const (
	ConfigKeyTracking         = "tracking"                    // "on"|"off"
	ConfigKeyHook             = "hook"                        // "on"|"off"
	ConfigKeyTelemetry        = "telemetry"                   // "on"|"off"|"auto" (auto = follow consent)
	ConfigKeyDaemonIdleSec    = "daemon.idle_timeout_seconds" // int
	ConfigKeyDaemonSocketPath = "daemon.socket_path"          // string override (tests)
	// ConfigKeyWorkflowDefaultMode controls the default write mode for
	// `browzer workflow <verb>` mutations when no per-call --async/--sync/
	// --await flag is passed. Values: "async"|"sync"|"await". Defaults to
	// "async" (fire-and-forget through the daemon, fall back to standalone
	// when the daemon is unreachable). Set to "sync" to force the historic
	// in-process write path; set to "await" to run through the daemon but
	// block on durability per call.
	ConfigKeyWorkflowDefaultMode = "workflow.default_mode" // "async"|"sync"|"await"
	// ConfigKeyDaemonWorkflowKeepaliveSec is how long the daemon's per-path
	// workflow drainer keeps its goroutine warm after the queue empties.
	// Default 1800s (30 min); 0 falls back to the dispatcher default.
	ConfigKeyDaemonWorkflowKeepaliveSec = "daemon.workflow_keepalive_seconds" // int
)

// --- Defaults ------------------------------------------------------------

const (
	DefaultDaemonIdleSeconds = 600
	DefaultTrackingOn        = true
	DefaultHookOn            = true
	// DefaultTelemetryMode = "auto" — flusher runs only when the
	// server-side `telemetry_consent_at` is non-null.
	DefaultTelemetryMode = "auto"
	// DefaultWorkflowMode is the default write mode when no flag overrides.
	// "async" maximises throughput for orchestrate-task-delivery's many
	// transient mutations; the `commit` skill explicitly opts into
	// `--await` to ensure the workflow.json reflects on disk before
	// `git commit`.
	DefaultWorkflowMode = "async"
	// DefaultWorkflowKeepaliveSeconds matches workflow_queue.go's
	// defaultQueueIdleTimeout (30 minutes). Tunable via
	// `daemon.workflow_keepalive_seconds`.
	DefaultWorkflowKeepaliveSeconds = 1800
)

// --- Env var names -------------------------------------------------------
//
// `EnvServer` ("BROWZER_SERVER") is declared in env.go and shared.

const (
	EnvHookDisable     = "BROWZER_HOOK"     // accepts "off","0","false"
	EnvTrackingDisable = "BROWZER_TRACKING" // accepts "off","0","false"
	EnvDaemonSocket    = "BROWZER_DAEMON_SOCKET"
)
