package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// stubEnv is the test injection point for the BROWZER_LLM environment
// variable. The production path passes realEnv{} which reads from the
// real process env; tests pass stubEnv{value, present} for full
// determinism without poking os.Setenv (which would race with t.Parallel
// callers in the same package).
type stubEnv struct {
	value string
}

func (s stubEnv) Get(key string) string {
	if key == "BROWZER_LLM" {
		return s.value
	}
	return ""
}

// setupWorkspaceFixture stages a temporary HOME with both the
// credentials sentinel and a `.browzer/config.json` so
// IsInBrowzerWorkspace returns true. It also redirects the session
// sentinel directory under the same HOME, which gives each test a
// clean once-per-session slate. The test's working directory is moved
// into the workspace fixture so the walk-up succeeds.
func setupWorkspaceFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()

	browzerHome := filepath.Join(tmp, "home", ".browzer")
	if err := os.MkdirAll(browzerHome, 0o700); err != nil {
		t.Fatalf("mkdir browzer home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(browzerHome, "credentials"), []byte("stub"), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	wsRoot := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(filepath.Join(wsRoot, ".browzer"), 0o700); err != nil {
		t.Fatalf("mkdir workspace .browzer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsRoot, ".browzer", "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}

	t.Setenv("HOME", filepath.Join(tmp, "home"))

	// Hard-disable the daemon-respawn fallback inside this test's process.
	// Without this guard, hook decide functions that call TrackEvent /
	// EmitHookDelta indirectly invoke EnsureDaemon → fork a real
	// `browzer daemon start --background` child that inherits this test's
	// tempdir HOME, opens a tracker DB under <tempdir>/home/.browzer/
	// history.db, and SURVIVES the test process because the spawn is
	// detached. Subsequent CLI calls in the same OS session bind to the
	// orphan daemon and silently route production telemetry into the test
	// tempdir. See internal/hooks/daemon_call.go::EnsureDaemon for the
	// matching env-var check.
	t.Setenv("BROWZER_HOOK_NO_DAEMON_SPAWN", "1")

	// Move cwd into the workspace fixture so the walk-up matches.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(wsRoot); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	// Force a unique session key per test so SessionBannerEmittedOnce
	// always sees a fresh slate.
	t.Setenv("CLAUDE_SESSION_ID", t.Name())

	return wsRoot
}

// runDecide drives rewriteBashDecide directly with a stub env. Returns
// the exit code and any envelope written to stdout (nil if none).
func runDecide(t *testing.T, input string, env envLookup) (int, *rewriteEnvelope) {
	t.Helper()
	var buf bytes.Buffer
	code := rewriteBashDecide([]byte(input), &buf, env)
	if buf.Len() == 0 {
		return code, nil
	}
	var got rewriteEnvelope
	// json.Decoder tolerates the trailing newline from Encoder.
	dec := json.NewDecoder(&buf)
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode envelope: %v\nraw=%q", err, buf.String())
	}
	return code, &got
}

func mustMarshalCommand(t *testing.T, command string) string {
	t.Helper()
	in := map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
		"session_id": "smoke",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestRewriteBash_BrowzerCommand_InjectsLLMEnv(t *testing.T) {
	setupWorkspaceFixture(t)
	code, env := runDecide(t, mustMarshalCommand(t, "browzer explore foo"), stubEnv{})
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d, want %d (allow)", code, internalhooks.ExitAllow)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected rewrite envelope")
	}
	got, _ := env.HookSpecificOutput.UpdatedInput["command"].(string)
	if got != "BROWZER_LLM=1 browzer explore foo" {
		t.Errorf("rewritten command = %q", got)
	}
	if env.HookSpecificOutput.AdditionalContext == "" {
		t.Error("first-emit banner missing from additionalContext")
	}
	if env.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("permissionDecision = %q, want allow", env.HookSpecificOutput.PermissionDecision)
	}
}

func TestRewriteBash_BrowzerCommand_AlreadyHasEnvNoDoubleInject(t *testing.T) {
	setupWorkspaceFixture(t)
	// `BROWZER_LLM=0 browzer explore foo` — the leading shell token
	// is the env assignment, not the literal `browzer` word, so
	// browzerCommandRE does NOT match. Execution falls through every
	// rewrite branch and lands on the final passthrough. The brief
	// permits either ExitAllow (matching the JS bare `process.exit(0)`)
	// or ExitPassthrough; the Go port chooses the more conservative
	// passthrough so Claude Code is free to keep its default.
	code, env := runDecide(t, mustMarshalCommand(t, "BROWZER_LLM=0 browzer explore foo"), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want %d", code, internalhooks.ExitPassthrough)
	}
	if env != nil {
		t.Errorf("no envelope expected on opt-out branch, got %+v", env)
	}
}

func TestRewriteBash_BrowzerCommand_LLMFlagNoInject(t *testing.T) {
	setupWorkspaceFixture(t)
	code, env := runDecide(t, mustMarshalCommand(t, "browzer search --llm foo"), stubEnv{})
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d", code)
	}
	if env != nil {
		t.Errorf("no envelope expected when --llm already present")
	}
}

func TestRewriteBash_BrowzerCommand_SubshellNoInject(t *testing.T) {
	setupWorkspaceFixture(t)
	// `(browzer explore foo)` — leading `(` means browzerCommandRE
	// doesn't match (the regex requires literal `browzer` after
	// optional whitespace). The wrappedSubshell guard at the JS source
	// is technically redundant for this exact shape but defends
	// against ` ( browzer …` style with leading whitespace + paren.
	// Either way the result is "no rewrite, passthrough".
	code, env := runDecide(t, mustMarshalCommand(t, "(browzer explore foo)"), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d", code)
	}
	if env != nil {
		t.Errorf("no envelope expected for subshell-wrapped browzer call")
	}
}

func TestRewriteBash_BrowzerCommand_EnvTruthySuppressesBanner(t *testing.T) {
	setupWorkspaceFixture(t)
	// Env says BROWZER_LLM is already truthy in the parent shell —
	// the guard injects the prefix anyway (the CLI still needs it on
	// the child) but suppresses the banner.
	code, env := runDecide(t, mustMarshalCommand(t, "browzer explore foo"), stubEnv{value: "1"})
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d", code)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected envelope")
	}
	if env.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("banner should be suppressed when env BROWZER_LLM is truthy; got %q",
			env.HookSpecificOutput.AdditionalContext)
	}
}

func TestRewriteBash_BrowzerCommand_EnvFalsyEmitsBanner(t *testing.T) {
	setupWorkspaceFixture(t)
	for _, val := range []string{"", "0", "false"} {
		t.Run("env="+val, func(t *testing.T) {
			// Each sub-test gets its own session key so the sentinel
			// is fresh — otherwise the first sub-test would consume
			// the once-per-session emission and the rest would see a
			// suppressed banner.
			t.Setenv("CLAUDE_SESSION_ID", t.Name())
			code, env := runDecide(t, mustMarshalCommand(t, "browzer explore foo"), stubEnv{value: val})
			if code != internalhooks.ExitAllow {
				t.Fatalf("exit code = %d", code)
			}
			if env == nil || env.HookSpecificOutput == nil {
				t.Fatal("expected envelope")
			}
			if env.HookSpecificOutput.AdditionalContext == "" {
				t.Errorf("banner expected when env BROWZER_LLM=%q", val)
			}
		})
	}
}

func TestRewriteBash_RunProxy_GitStatus(t *testing.T) {
	setupWorkspaceFixture(t)
	code, env := runDecide(t, mustMarshalCommand(t, "git status"), stubEnv{})
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d", code)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected envelope")
	}
	got, _ := env.HookSpecificOutput.UpdatedInput["command"].(string)
	if got != "browzer run git status" {
		t.Errorf("rewritten command = %q", got)
	}
	if env.HookSpecificOutput.AdditionalContext == "" {
		t.Error("first-emit run-proxy banner missing")
	}
}

func TestRewriteBash_RunProxy_PipeEmitsHint(t *testing.T) {
	setupWorkspaceFixture(t)
	code, env := runDecide(t, mustMarshalCommand(t, "git status | head"), stubEnv{})
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d", code)
	}
	if env == nil {
		t.Fatal("expected hint envelope")
	}
	if env.HookSpecificOutput != nil {
		t.Errorf("hint branch must not nest hookSpecificOutput; got %+v", env.HookSpecificOutput)
	}
	if !strings.Contains(env.AdditionalContext, "browzer run git status | head") {
		t.Errorf("hint missing manual-rewrite suggestion: %q", env.AdditionalContext)
	}
}

func TestRewriteBash_RunProxy_ChainOpSilentSkip(t *testing.T) {
	setupWorkspaceFixture(t)
	code, env := runDecide(t, mustMarshalCommand(t, "git status && echo ok"), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("chain-op compound must emit no envelope, got %+v", env)
	}
}

func TestRewriteBash_RunProxy_AlreadyWrapped(t *testing.T) {
	setupWorkspaceFixture(t)
	// `browzer run` skips both the LLM-injection branch (leading
	// token IS `browzer`, but actually it does match browzerCommandRE —
	// the LLM-injection branch runs first and prepends the env). To
	// avoid that interaction we test with a leading-token-not-browzer
	// command that just happens to use browzer run anyway; that test
	// covers the inner skip predicate.
	code, env := runDecide(t, mustMarshalCommand(t, "browzer run git status"), stubEnv{})
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d", code)
	}
	// Leading `browzer` triggers the LLM-injection branch, which
	// rewrites the command to `BROWZER_LLM=1 browzer run git status`.
	// The run-proxy branch is correctly skipped because exit happens
	// inside the LLM block; assert that's the shape we got.
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected envelope")
	}
	got, _ := env.HookSpecificOutput.UpdatedInput["command"].(string)
	if got != "BROWZER_LLM=1 browzer run git status" {
		t.Errorf("rewritten command = %q", got)
	}
}

func TestRewriteBash_NeverRewritePath(t *testing.T) {
	setupWorkspaceFixture(t)
	// `cat /etc/Dockerfile` matches the cat/head/tail regex BUT
	// `Dockerfile` is on the NEVER_REWRITE list — passthrough.
	code, env := runDecide(t, mustMarshalCommand(t, "cat /etc/Dockerfile"), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("never-rewrite path must emit no envelope")
	}
}

func TestRewriteBash_CatSmallFileSkipped(t *testing.T) {
	setupWorkspaceFixture(t)
	// Create a real small .go file; the cat fallback rejects sizes
	// below 40 KB.
	dir := t.TempDir()
	small := filepath.Join(dir, "small.go")
	if err := os.WriteFile(small, []byte("package x\n"), 0o600); err != nil {
		t.Fatalf("write small file: %v", err)
	}
	code, env := runDecide(t, mustMarshalCommand(t, "cat "+small), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("small file must emit no envelope")
	}
}

func TestRewriteBash_CatLargeCodeFileRewritten(t *testing.T) {
	setupWorkspaceFixture(t)
	dir := t.TempDir()
	big := filepath.Join(dir, "big.go")
	// 50 KB of padding so the >=40 KB gate trips.
	payload := append([]byte("package x\n"), bytes.Repeat([]byte("// pad\n"), 8000)...)
	if err := os.WriteFile(big, payload, 0o600); err != nil {
		t.Fatalf("write big file: %v", err)
	}
	code, env := runDecide(t, mustMarshalCommand(t, "cat "+big), stubEnv{})
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d, want allow", code)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected envelope")
	}
	got, _ := env.HookSpecificOutput.UpdatedInput["command"].(string)
	wantPrefix := "browzer read "
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, " --filter=auto") {
		t.Errorf("rewritten command shape unexpected: %q", got)
	}
}

func TestRewriteBash_GrepPassesThrough(t *testing.T) {
	setupWorkspaceFixture(t)
	// `grep -r foo .` matches no rewrite pattern → passthrough.
	code, env := runDecide(t, mustMarshalCommand(t, "grep -r foo ."), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("grep must emit no envelope, got %+v", env)
	}
}

func TestRewriteBash_NonBashToolPassesThrough(t *testing.T) {
	setupWorkspaceFixture(t)
	in := map[string]any{
		"tool_name":  "Edit",
		"tool_input": map[string]any{"command": "browzer explore foo"},
		"session_id": "smoke",
	}
	b, _ := json.Marshal(in)
	code, env := runDecide(t, string(b), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("non-Bash tool must emit no envelope")
	}
}

func TestRewriteBash_GateDisabledPassesThrough(t *testing.T) {
	setupWorkspaceFixture(t)
	t.Setenv("BROWZER_HOOK", "off")
	code, env := runDecide(t, mustMarshalCommand(t, "browzer explore foo"), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("gate-disabled run must emit no envelope")
	}
}

func TestRewriteBash_PerHookDisablePassesThrough(t *testing.T) {
	setupWorkspaceFixture(t)
	t.Setenv("BROWZER_HOOK_DISABLE", "rewrite-bash,prompt-guard")
	code, env := runDecide(t, mustMarshalCommand(t, "browzer explore foo"), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("per-hook-disable run must emit no envelope")
	}
}

func TestRewriteBash_OutsideWorkspacePassesThrough(t *testing.T) {
	// Deliberately skip setupWorkspaceFixture so IsInBrowzerWorkspace
	// returns false.
	tmp := t.TempDir()
	// Point HOME at a directory without credentials so the workspace
	// check fails fast.
	t.Setenv("HOME", tmp)
	origWD, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	code, env := runDecide(t, mustMarshalCommand(t, "browzer explore foo"), stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("outside-workspace run must emit no envelope")
	}
}

func TestRewriteBash_MalformedJSONPassesThrough(t *testing.T) {
	setupWorkspaceFixture(t)
	code, env := runDecide(t, "{not json", stubEnv{})
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("exit code = %d, want passthrough", code)
	}
	if env != nil {
		t.Errorf("malformed JSON must emit no envelope")
	}
}

// TestRewriteBash_TableDriven_QuickShape exercises a wider matrix in a
// single test for fast regression triage when a rewrite pattern is
// added.
func TestRewriteBash_TableDriven_QuickShape(t *testing.T) {
	setupWorkspaceFixture(t)
	cases := []struct {
		name         string
		command      string
		wantExit     int
		wantEnvelope bool
	}{
		{"vitest standalone", "vitest run", internalhooks.ExitAllow, true},
		{"npx vitest", "npx vitest run", internalhooks.ExitAllow, true},
		{"pnpm turbo", "pnpm turbo test", internalhooks.ExitAllow, true},
		{"go test", "go test ./...", internalhooks.ExitAllow, true},
		{"cargo test", "cargo test --release", internalhooks.ExitAllow, true},
		{"biome check", "biome check .", internalhooks.ExitAllow, true},
		{"tsc", "tsc --noEmit", internalhooks.ExitAllow, true},
		{"ls passthrough", "ls -la", internalhooks.ExitPassthrough, false},
		{"echo passthrough", "echo hi", internalhooks.ExitPassthrough, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_SESSION_ID", t.Name())
			code, env := runDecide(t, mustMarshalCommand(t, tc.command), stubEnv{})
			if code != tc.wantExit {
				t.Fatalf("exit code = %d, want %d", code, tc.wantExit)
			}
			if tc.wantEnvelope && env == nil {
				t.Fatal("expected envelope")
			}
			if !tc.wantEnvelope && env != nil {
				t.Errorf("unexpected envelope: %+v", env)
			}
		})
	}
}
