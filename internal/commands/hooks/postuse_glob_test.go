package hooks

import (
	"encoding/json"
	"regexp"
	"testing"
)

// ---- globPatternToRE unit tests (pure, no I/O) ----

// TestGlobPatternToRE_StarGlob verifies that * matches a single path segment
// but not a slash.
func TestGlobPatternToRE_StarGlob(t *testing.T) {
	t.Parallel()

	re := globPatternToRE("src/*.ts")
	cases := []struct {
		path string
		want bool
	}{
		{"src/main.ts", true},
		{"src/lib.ts", true},
		{"src/nested/main.ts", false}, // * must not cross /
		{"other/main.ts", false},
	}
	for _, c := range cases {
		got := re.MatchString(c.path)
		if got != c.want {
			t.Errorf("glob %q vs path %q: MatchString = %v, want %v", "src/*.ts", c.path, got, c.want)
		}
	}
}

// TestGlobPatternToRE_DoubleStarGlob verifies ** matches across directory
// boundaries.
func TestGlobPatternToRE_DoubleStarGlob(t *testing.T) {
	t.Parallel()

	re := globPatternToRE("src/**/*.ts")
	cases := []struct {
		path string
		want bool
	}{
		{"src/main.ts", true},
		{"src/a/b/c/deep.ts", true},
		{"src/a/b/lib.ts", true},
		{"other/main.ts", false},
	}
	for _, c := range cases {
		got := re.MatchString(c.path)
		if got != c.want {
			t.Errorf("glob %q vs path %q: MatchString = %v, want %v", "src/**/*.ts", c.path, got, c.want)
		}
	}
}

// TestGlobPatternToRE_QuestionMark verifies ? matches a single non-slash char.
func TestGlobPatternToRE_QuestionMark(t *testing.T) {
	t.Parallel()

	re := globPatternToRE("src/?.ts")
	cases := []struct {
		path string
		want bool
	}{
		{"src/a.ts", true},
		{"src/ab.ts", false},  // ? matches exactly one char
		{"src//.ts", false},   // ? must not match /
	}
	for _, c := range cases {
		got := re.MatchString(c.path)
		if got != c.want {
			t.Errorf("glob %q vs path %q: MatchString = %v, want %v", "src/?.ts", c.path, got, c.want)
		}
	}
}

// TestGlobPatternToRE_REMetaEscaped verifies that regexp metacharacters in the
// glob pattern are properly escaped (e.g. a literal dot in the pattern must not
// act as the RE wildcard).
func TestGlobPatternToRE_REMetaEscaped(t *testing.T) {
	t.Parallel()

	re := globPatternToRE("foo.bar")
	// A literal dot pattern should match "foo.bar" but not "fooXbar".
	if !re.MatchString("foo.bar") {
		t.Error("glob with literal dot: expected match on 'foo.bar'")
	}
	if re.MatchString("fooXbar") {
		t.Error("glob with literal dot: unexpected match on 'fooXbar' (dot was not escaped)")
	}
}

// TestGlobPatternToRE_WindowsBackslash verifies that backslash separators
// (emitted by Claude Code on Windows agents) are normalised to forward-slashes
// before the regex is built so they match POSIX-canonical manifest paths.
func TestGlobPatternToRE_WindowsBackslash(t *testing.T) {
	t.Parallel()

	cases := []struct {
		glob string
		path string
		want bool
		desc string
	}{
		// Simple *.go pattern — no separators at all; must still work.
		{"*.go", "main.go", true, "bare *.go matches top-level .go file"},
		{"*.go", "sub/main.go", false, "bare *.go must not cross /"},
		// Windows-style sub\*.go — must match sub/main.go after normalisation.
		{`sub\*.go`, "sub/main.go", true, `sub\*.go normalised matches sub/main.go`},
		{`sub\*.go`, "other/main.go", false, `sub\*.go must not match other/`},
		// Deep Windows path with double-star equivalent.
		{`src\**\*.go`, "src/pkg/foo.go", true, `src\**\*.go matches deep POSIX path`},
		{`src\**\*.go`, "other/pkg/foo.go", false, `src\**\*.go must not match other/`},
	}
	for _, c := range cases {
		got := globPatternToRE(c.glob).MatchString(c.path)
		if got != c.want {
			t.Errorf("[%s] glob %q vs path %q: MatchString = %v, want %v",
				c.desc, c.glob, c.path, got, c.want)
		}
	}
}

// TestGlobPatternToRE_InvalidPattern verifies that an invalid glob pattern
// (e.g. one that would produce an invalid regexp) returns a non-nil regexp that
// matches nothing rather than panicking.
func TestGlobPatternToRE_InvalidPattern(t *testing.T) {
	t.Parallel()

	// Force a compile-error by passing a pattern that after transformation
	// would produce an invalid bracket expression.
	badRE := regexp.MustCompile(`^\x00$`) // the fallback sentinel
	got := globPatternToRE("[invalid")
	// The function must return a valid (non-nil) regexp.
	if got == nil {
		t.Fatal("globPatternToRE: expected non-nil regexp for bad pattern")
	}
	// The fallback must match nothing sensible.
	if got.String() != badRE.String() && got.MatchString("anything") {
		t.Errorf("globPatternToRE bad pattern: matched 'anything' unexpectedly")
	}
}

// ---- postuseGlobDecide gate tests ----

// makePostuseGlobInput builds a minimal PostToolUse(Glob) JSON payload.
func makePostuseGlobInput(t *testing.T, toolName, pattern string) []byte {
	t.Helper()
	payload := map[string]any{
		"tool_name": toolName,
		"tool_input": map[string]any{
			"pattern": pattern,
		},
		"tool_response": map[string]any{
			"content": "",
		},
		"session_id": t.Name(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("makePostuseGlobInput: marshal failed: %v", err)
	}
	return data
}

// TestPostuseGlobDecide_GateOff_False verifies the BROWZER_HOOK=off kill-switch
// suppresses telemetry.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestPostuseGlobDecide_GateOff_False(t *testing.T) {
	setupWorkspaceFixture(t)
	t.Setenv("BROWZER_HOOK", "off")

	raw := makePostuseGlobInput(t, "Glob", "src/**/*.ts")
	if postuseGlobDecide(raw) {
		t.Error("gate off: expected false (no telemetry)")
	}
}

// TestPostuseGlobDecide_NonGlobTool_False verifies that non-Glob tools return
// false without emitting telemetry.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestPostuseGlobDecide_NonGlobTool_False(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makePostuseGlobInput(t, "Bash", "src/**/*.ts")
	if postuseGlobDecide(raw) {
		t.Error("non-Glob tool: expected false")
	}
}

// TestPostuseGlobDecide_ConfigSurface_False verifies that patterns targeting
// the config surface are passed through without telemetry.
// ConfigSurfaceRE matches paths containing .github, .claude, .vscode, etc.
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestPostuseGlobDecide_ConfigSurface_False(t *testing.T) {
	setupWorkspaceFixture(t)

	payload := map[string]any{
		"tool_name": "Glob",
		"tool_input": map[string]any{
			"path":    ".github/workflows",
			"pattern": "*.yml",
		},
		"tool_response": map[string]any{"content": ""},
		"session_id":    t.Name(),
	}
	data, _ := json.Marshal(payload)

	if postuseGlobDecide(data) {
		t.Error("config surface: expected false (no telemetry)")
	}
}

// TestPostuseGlobDecide_GlobInWorkspace_True verifies that a normal Glob call
// inside a Browzer workspace returns true (telemetry submitted).
//
// Note: setupWorkspaceFixture calls t.Setenv — t.Parallel() would panic.
func TestPostuseGlobDecide_GlobInWorkspace_True(t *testing.T) {
	setupWorkspaceFixture(t)

	raw := makePostuseGlobInput(t, "Glob", "src/**/*.ts")
	if !postuseGlobDecide(raw) {
		t.Error("normal Glob in workspace: expected true (telemetry submitted)")
	}
}
