package config

// Single source of truth for every persistent path, env var, and config key
// shared by the Browzer CLI binary and the Browzer daemon.
//
// The Node hooks (packages/skills/hooks/guards/*.mjs) do NOT import from
// here — they call the daemon over its Unix socket, which encapsulates
// these constants.
//
// Spec: docs/superpowers/specs/2026-04-15-cli-token-economy-design.md §7.3.

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
func SocketPath(uid int) string {
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
)

// --- Defaults ------------------------------------------------------------

const (
	DefaultDaemonIdleSeconds = 600
	DefaultTrackingOn        = true
	DefaultHookOn            = true
	// DefaultTelemetryMode = "auto" — flusher runs only when the
	// server-side `telemetry_consent_at` is non-null.
	DefaultTelemetryMode = "auto"
)

// --- Env var names -------------------------------------------------------
//
// `EnvServer` ("BROWZER_SERVER") is declared in env.go and shared.

const (
	EnvHookDisable     = "BROWZER_HOOK"     // accepts "off","0","false"
	EnvTrackingDisable = "BROWZER_TRACKING" // accepts "off","0","false"
	EnvDaemonSocket    = "BROWZER_DAEMON_SOCKET"
)
