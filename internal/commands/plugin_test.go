package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMockClaudeJSON writes a minimal ~/.claude.json-shaped file to a temp
// directory and returns a getCwdFn that returns the provided cwd value.
func writeMockClaudeJSON(t *testing.T, path string, content any) {
	t.Helper()
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal mock claude.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write mock claude.json: %v", err)
	}
}

// TestPluginDoctor_FailsOnEmptyEnabledPlugins asserts that doctor exits 1 and
// outputs FAIL mentioning "enabledPlugins" when the project's enabledPlugins
// map is empty for the working directory.
func TestPluginDoctor_FailsOnEmptyEnabledPlugins(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "myproject")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	// Minimal ~/.claude.json with an empty enabledPlugins for cwd.
	claudeJSONPath := filepath.Join(tmp, "home", ".claude.json")
	writeMockClaudeJSON(t, claudeJSONPath, map[string]any{
		"projects": map[string]any{
			cwd: map[string]any{
				"enabledPlugins": map[string]any{},
			},
		},
	})

	// Override readClaudeJSON behaviour by providing a test-friendly
	// implementation: rebuild the command and supply a getCwdFn returning cwd,
	// then monkey-patch the home dir via the env var the test controls.
	t.Setenv("HOME", filepath.Join(tmp, "home"))

	cmd := newPluginDoctorCommand(func() (string, error) { return cwd, nil })
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	out := buf.String()

	if err == nil {
		t.Error("expected non-nil error (exit 1) when enabledPlugins is empty, got nil")
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL in output, got: %q", out)
	}
	if !strings.Contains(out, "enabledPlugins") {
		t.Errorf("expected 'enabledPlugins' in FAIL message, got: %q", out)
	}
}

// TestPluginDoctor_WarnDoesNotCauseNonZeroExit asserts that a WARN line
// (e.g. missing ~/.claude.json) does NOT set exit code 1. Only FAIL lines
// should trigger a non-zero exit (exit-code contract, F-016).
func TestPluginDoctor_WarnDoesNotCauseNonZeroExit(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "myproject")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	homeDir := filepath.Join(tmp, "home")

	// Provide installed_plugins.json so check 1 passes.
	pluginsDir := filepath.Join(homeDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(pluginsDir, "installed_plugins.json")
	if err := os.WriteFile(installedPath, []byte(`{"browzer": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Do NOT write ~/.claude.json so check 2 emits WARN (file not found).
	t.Setenv("HOME", homeDir)

	cmd := newPluginDoctorCommand(func() (string, error) { return cwd, nil })
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	out := buf.String()

	if err != nil {
		t.Errorf("WARN-only run must exit 0, got error: %v\noutput: %q", err, out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN line for missing ~/.claude.json, got: %q", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("expected no FAIL lines in WARN-only run, got: %q", out)
	}
}

// TestCheckPluginInstalled_CompositeKey asserts that checkPluginInstalled
// recognises composite keys such as "browzer@browzer-marketplace" (case-
// insensitive substring match, not exact equality). Covers F-031.
func TestCheckPluginInstalled_CompositeKey(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	pluginsDir := filepath.Join(homeDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(pluginsDir, "installed_plugins.json")

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"composite key", `{"browzer@browzer-marketplace": {}}`, true},
		{"upper-case key", `{"BROWZER": {}}`, true},
		{"mixed-case composite", `{"Browzer@Marketplace": {}}`, true},
		{"unrelated key", `{"other-plugin": {}}`, false},
		{"empty", `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(installedPath, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", homeDir)
			got := checkPluginInstalled()
			if got != tc.want {
				t.Errorf("checkPluginInstalled() = %v, want %v (key content: %s)", got, tc.want, tc.content)
			}
		})
	}
}

// TestPluginDoctor_PassesOnHealthyConfig asserts that doctor exits 0 and
// outputs only PASS lines when the plugin is correctly enabled.
func TestPluginDoctor_PassesOnHealthyConfig(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "myproject")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	homeDir := filepath.Join(tmp, "home")

	// Create installed_plugins.json with a browzer key.
	pluginsDir := filepath.Join(homeDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(pluginsDir, "installed_plugins.json")
	if err := os.WriteFile(installedPath, []byte(`{"browzer": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// ~/.claude.json with a valid browzer entry for cwd.
	claudeJSONPath := filepath.Join(homeDir, ".claude.json")
	writeMockClaudeJSON(t, claudeJSONPath, map[string]any{
		"projects": map[string]any{
			cwd: map[string]any{
				"enabledPlugins": map[string]any{
					"browzer@browzer-marketplace": true,
				},
			},
		},
	})

	t.Setenv("HOME", homeDir)

	cmd := newPluginDoctorCommand(func() (string, error) { return cwd, nil })
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	out := buf.String()

	if err != nil {
		t.Errorf("expected nil error (exit 0) on healthy config, got: %v\noutput: %q", err, out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("expected no FAIL lines in healthy output, got: %q", out)
	}
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected at least one PASS line in output, got: %q", out)
	}
}
