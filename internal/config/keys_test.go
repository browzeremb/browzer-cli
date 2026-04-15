package config

import (
	"strings"
	"testing"
)

func TestSocketPath_FormatsWithUid(t *testing.T) {
	p := SocketPath(501)
	if !strings.Contains(p, "browzer-daemon.501.sock") {
		t.Fatalf("SocketPath(501) = %q, want it to contain 'browzer-daemon.501.sock'", p)
	}
	if !strings.HasPrefix(p, "/tmp/") {
		t.Fatalf("SocketPath should live under /tmp; got %q", p)
	}
}

func TestConfigKeys_AreDistinct(t *testing.T) {
	keys := []string{
		ConfigKeyTracking,
		ConfigKeyHook,
		ConfigKeyTelemetry,
		ConfigKeyDaemonIdleSec,
		ConfigKeyDaemonSocketPath,
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("duplicate config key %q", k)
		}
		seen[k] = true
	}
}

func TestDefaults_HaveExpectedTypes(t *testing.T) {
	if DefaultDaemonIdleSeconds != 600 {
		t.Fatalf("DefaultDaemonIdleSeconds = %d, want 600", DefaultDaemonIdleSeconds)
	}
	if DefaultTrackingOn != true {
		t.Fatal("DefaultTrackingOn must be true")
	}
	if DefaultHookOn != true {
		t.Fatal("DefaultHookOn must be true")
	}
}

func TestPathHelpers_ReturnAbsolutePaths(t *testing.T) {
	for name, p := range map[string]string{
		"HistoryDBPath":      HistoryDBPath(),
		"ConfigPath":         ConfigPath(),
		"PIDPath":            PIDPath(),
		"TelemetryStatePath": TelemetryStatePath(),
	} {
		if !strings.HasPrefix(p, "/") {
			t.Fatalf("%s = %q, want absolute path", name, p)
		}
	}
	if !strings.HasSuffix(SessionCachePath("sess_1"), "/sessions/sess_1.json") {
		t.Fatalf("SessionCachePath has wrong suffix: %q", SessionCachePath("sess_1"))
	}
	if !strings.HasSuffix(ManifestCachePath("ws_1"), "/workspaces/ws_1/manifest.json") {
		t.Fatalf("ManifestCachePath has wrong suffix: %q", ManifestCachePath("ws_1"))
	}
}
