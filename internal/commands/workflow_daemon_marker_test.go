package commands

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/config"
)

// TestDaemonDownMarker_TTLBehaviour (B3, 2026-05-05): the marker
// suppresses warns inside the TTL window and falls through (returns
// false) once the file is older than daemonDownMarkerTTL. Touch +
// clear are idempotent.
func TestDaemonDownMarker_TTLBehaviour(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvHome, dir)

	// Initial state: no marker → not active.
	if daemonDownMarkerActive() {
		t.Fatalf("marker active before any touch — expected absent")
	}

	// Touch creates the file with mtime≈now.
	daemonDownMarkerTouch()
	path := config.DaemonDownMarkerPath()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("marker not created: %v (path=%s)", err, path)
	}
	if !daemonDownMarkerActive() {
		t.Errorf("marker should be active immediately after touch")
	}

	// Backdate to expire the TTL.
	stale := time.Now().Add(-2 * daemonDownMarkerTTL)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if daemonDownMarkerActive() {
		t.Errorf("marker should be stale after backdating mtime past TTL")
	}

	// Re-touch refreshes mtime.
	daemonDownMarkerTouch()
	if !daemonDownMarkerActive() {
		t.Errorf("marker should be active after re-touch")
	}

	// Clear removes the file.
	daemonDownMarkerClear()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("marker not removed by clear (err=%v)", err)
	}
	if daemonDownMarkerActive() {
		t.Errorf("marker should be inactive after clear")
	}

	// Clear is idempotent.
	daemonDownMarkerClear()
}

// TestDaemonDownMarker_HonoursBROWZER_HOME (B3, 2026-05-05): when
// BROWZER_HOME is set the marker lives at $BROWZER_HOME/.daemon-down-marker
// (not in ~/.browzer/). Required so test isolation doesn't bleed
// into real operator state.
func TestDaemonDownMarker_HonoursBROWZER_HOME(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvHome, dir)
	got := config.DaemonDownMarkerPath()
	want := filepath.Join(dir, ".daemon-down-marker")
	if got != want {
		t.Errorf("marker path: got %q want %q", got, want)
	}
}
