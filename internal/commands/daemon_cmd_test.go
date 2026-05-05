package commands

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

func TestCptFor_ManifestLangWins(t *testing.T) {
	// Manifest language is authoritative when present and known.
	if got := cptFor("typescript", "/path/file.go"); got != 2.39 {
		t.Errorf("manifest=typescript cpt=%.2f, want 2.39", got)
	}
	if got := cptFor("go", "/path/file.ts"); got != 2.15 {
		t.Errorf("manifest=go cpt=%.2f, want 2.15", got)
	}
}

func TestCptFor_UnknownManifestFallsBackToExt(t *testing.T) {
	// Unknown manifest language falls through to extension lookup.
	if got := cptFor("rust", "/src/main.go"); got != 2.15 {
		t.Errorf("unknown manifest + .go ext cpt=%.2f, want 2.15 (go)", got)
	}
	if got := cptFor("c++", "/x.ts"); got != 2.39 {
		t.Errorf("unknown manifest + .ts ext cpt=%.2f, want 2.39 (typescript)", got)
	}
}

func TestCptFor_NoManifestUsesExt(t *testing.T) {
	cases := []struct {
		path string
		want float64
	}{
		{"/a/b.ts", 2.39},
		{"/a/b.tsx", 2.39},
		{"/a/b.js", 2.22},
		{"/a/b.mjs", 2.22},
		{"/a/b.go", 2.15},
		{"/a/b.py", 2.79},
		{"/a/b.md", 2.56},
		{"/a/b.json", 1.97},
		{"/a/b.yaml", 2.36},
		{"/a/b.yml", 2.36},
		{"/a/B.TS", 2.39}, // case-insensitive ext
	}
	for _, c := range cases {
		if got := cptFor("", c.path); got != c.want {
			t.Errorf("path=%s cpt=%.3f, want %.2f", c.path, got, c.want)
		}
	}
}

func TestCptFor_UnknownExtFallsBackToDefault(t *testing.T) {
	cases := []string{
		"/a/b.rs",
		"/a/b.lua",
		"/a/b.sh",
		"/a/no-ext",
		"",
	}
	for _, p := range cases {
		if got := cptFor("", p); got != defaultCharsPerToken {
			t.Errorf("path=%q cpt=%.3f, want default %.2f", p, got, defaultCharsPerToken)
		}
	}
}

// TestDaemonStatus_ExitsOneWhenNoDaemon asserts WF-CLI-UX-3: the
// command must return a non-nil error mapping to exit code 1 when
// the daemon socket is unreachable. Otherwise the orchestrator
// skill's `browzer daemon status … || browzer daemon start …`
// pre-warm never fires (regression observed 2026-05-04 shakedown).
func TestDaemonStatus_ExitsOneWhenNoDaemon(t *testing.T) {
	// Point the daemon socket at a path inside a fresh temp dir so
	// no concurrent daemon (real or otherwise) interferes.
	tmp := t.TempDir()
	t.Setenv("BROWZER_DAEMON_SOCKET", filepath.Join(tmp, "missing.sock"))

	cmd := daemonStatusCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-nil error when daemon is unreachable; got nil. stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var ce *cliErrors.CliError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *cliErrors.CliError; got %T (%v)", err, err)
	}
	if ce.ExitCode != cliErrors.ExitError {
		t.Errorf("expected exit code %d, got %d", cliErrors.ExitError, ce.ExitCode)
	}
	if !strings.Contains(stdout.String(), "not running") {
		t.Errorf("expected stdout to include 'not running', got %q", stdout.String())
	}
}

// TestSavedTokens_RegressionAgainstOldFormula documents the expected
// shift in reported savings vs the pre-2026-04-17 `/4` constant. Guards
// against someone silently reverting to the old divisor: for a TS byte
// delta of 1000, the new formula returns ~418, the old returned 250.
// If this test fails, inspect the calibration data before "fixing" it.
func TestSavedTokens_RegressionAgainstOldFormula(t *testing.T) {
	byteDelta := 1000
	// TS, calibrated
	tsCPT := cptFor("typescript", "/x.ts")
	newTS := int(float64(byteDelta) / tsCPT)
	oldTS := byteDelta / 4
	if newTS <= oldTS {
		t.Fatalf("TS: new formula (%d) should exceed old /4 (%d) — cpt=%.2f", newTS, oldTS, tsCPT)
	}
	// JSON is densest — even larger shift
	jsonCPT := cptFor("json", "/x.json")
	newJSON := int(float64(byteDelta) / jsonCPT)
	if newJSON <= newTS {
		t.Fatalf("JSON shift (%d) should exceed TS shift (%d)", newJSON, newTS)
	}
}
