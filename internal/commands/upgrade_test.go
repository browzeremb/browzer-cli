package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/api"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// fakeRelease serves a canned GitHub release payload and records the
// request it received. Returns a teardown that restores
// api.LatestReleaseURL — call via t.Cleanup.
func fakeRelease(t *testing.T, tag string) func() {
	t.Helper()
	payload := map[string]any{
		"tag_name":     tag,
		"html_url":     "https://example.test/release/" + tag,
		"published_at": "2026-04-10T00:00:00Z",
		"body":         "notes",
		"prerelease":   false,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	prev := api.LatestReleaseURL
	api.LatestReleaseURL = srv.URL
	return func() {
		api.LatestReleaseURL = prev
		srv.Close()
	}
}

func TestUpgrade_Registered(t *testing.T) {
	root := NewRootCommand("v0.1.0")
	cmd, _, err := root.Find([]string{"upgrade"})
	if err != nil {
		t.Fatalf("find upgrade: %v", err)
	}
	if cmd.Short == "" {
		t.Error("upgrade has empty Short description")
	}
	for _, name := range []string{"json", "save", "schema", "check"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("upgrade missing --%s flag", name)
		}
	}
}

func TestUpgrade_SchemaFlag(t *testing.T) {
	root := NewRootCommand("v0.1.0")
	root.SetArgs([]string{"upgrade", "--schema"})
	if err := root.Execute(); err != nil {
		t.Fatalf("upgrade --schema: %v", err)
	}
}

func TestUpgrade_CheckCurrent_ExitsZero(t *testing.T) {
	defer fakeRelease(t, "v1.2.3")()

	root := NewRootCommand("v1.2.3")
	root.SetArgs([]string{"upgrade", "--check", "--json"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("upgrade --check returned error for current build: %v", err)
	}
}

func TestUpgrade_CheckOutdated_ExitsTen(t *testing.T) {
	defer fakeRelease(t, "v1.3.0")()

	root := NewRootCommand("v1.2.3")
	root.SetArgs([]string{"upgrade", "--check", "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("upgrade --check should fail when outdated")
	}
	var cliErr *cliErrors.CliError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *CliError", err)
	}
	if cliErr.ExitCode != cliErrors.ExitOutdated {
		t.Errorf("exit code = %d, want %d", cliErr.ExitCode, cliErrors.ExitOutdated)
	}
}

func TestUpgrade_JSONPayloadShape(t *testing.T) {
	defer fakeRelease(t, "v2.0.0")()

	path := t.TempDir() + "/upgrade.json"
	root := NewRootCommand("v1.9.9")
	root.SetArgs([]string{"upgrade", "--json", "--save", path})
	if err := root.Execute(); err != nil {
		t.Fatalf("upgrade --save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"current", "latest", "outdated", "installChannel", "upgradeCommand"} {
		if _, ok := got[key]; !ok {
			t.Errorf("payload missing %q key: %v", key, got)
		}
	}
	if got["current"] != "v1.9.9" {
		t.Errorf("current = %v, want v1.9.9", got["current"])
	}
	if got["latest"] != "v2.0.0" {
		t.Errorf("latest = %v, want v2.0.0", got["latest"])
	}
	if got["outdated"] != true {
		t.Errorf("outdated = %v, want true", got["outdated"])
	}
}

func TestIsOutdated(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v1.2.3", "v1.2.3", false},
		{"1.2.3", "v1.2.3", false},
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", true},
		{"v1.2.3", "v2.0.0", true},
		{"v2.0.0", "v1.9.9", false},
		{"dev", "v1.0.0", true},
		{"", "v1.0.0", true},
		{"v1.0.0-rc1", "v1.0.0", true},
		{"v1.0.0", "v1.0.0-rc1", false},
	}
	for _, tc := range tests {
		got := isOutdated(tc.current, tc.latest)
		if got != tc.want {
			t.Errorf("isOutdated(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestDetectInstallChannel_Fallback(t *testing.T) {
	// We can't override os.Executable() without monkey-patching, so we
	// just assert the function returns a non-empty channel + command.
	// Channel-specific branches are covered by the classifier helper
	// test below (pure function, no OS dep).
	ch, cmd := detectInstallChannel()
	if ch == "" || cmd == "" {
		t.Errorf("detectInstallChannel() = (%q, %q), want non-empty", ch, cmd)
	}
}

// classifyPath is the pure-function slice of detectInstallChannel exposed
// here via a tiny reimplementation — keeps the channel-detection logic
// testable without monkeypatching os.Executable. Mirrors the switch in
// detectInstallChannel; if that logic changes, this table must too.
func classifyPath(p, goBinDir string) string {
	switch {
	case strings.Contains(p, string(filepath.Separator)+"Cellar"+string(filepath.Separator)),
		strings.HasPrefix(p, "/opt/homebrew/"),
		strings.HasPrefix(p, "/usr/local/Cellar/"),
		strings.HasPrefix(p, "/home/linuxbrew/.linuxbrew/"):
		return "homebrew"
	case strings.Contains(p, string(filepath.Separator)+"scoop"+string(filepath.Separator)):
		return "scoop"
	}
	if goBinDir != "" && strings.HasPrefix(p, goBinDir+string(filepath.Separator)) {
		return "go"
	}
	return "curl"
}

func TestChannelClassifier(t *testing.T) {
	tests := []struct {
		path, goBin, want string
	}{
		{filepath.FromSlash("/opt/homebrew/bin/browzer"), "", "homebrew"},
		{filepath.FromSlash("/usr/local/Cellar/browzer/0.1.0/bin/browzer"), "", "homebrew"},
		{filepath.FromSlash("/home/linuxbrew/.linuxbrew/bin/browzer"), "", "homebrew"},
		// classifyPath searches using filepath.Separator, so fixtures are
		// built via filepath.FromSlash to keep the test portable (Windows
		// CI rewrites `/scoop/` → `\scoop\`).
		{filepath.FromSlash("/home/alice/scoop/apps/browzer/current/browzer"), "", "scoop"},
		{filepath.FromSlash("/home/alice/go/bin/browzer"), filepath.FromSlash("/home/alice/go/bin"), "go"},
		{filepath.FromSlash("/home/alice/.local/bin/browzer"), filepath.FromSlash("/home/alice/go/bin"), "curl"},
	}
	for _, tc := range tests {
		got := classifyPath(tc.path, tc.goBin)
		if got != tc.want {
			t.Errorf("classifyPath(%q, %q) = %q, want %q", tc.path, tc.goBin, got, tc.want)
		}
	}
}

