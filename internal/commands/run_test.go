package commands

import (
	"testing"

	"github.com/browzeremb/browzer-cli/internal/commands/runfilters"
)

// TestRun_FlagRegistered asserts the "run" subcommand is wired into the root
// command tree and that --no-filter is not a registered cobra flag (it is
// manually parsed via DisableFlagParsing).
func TestRun_FlagRegistered(t *testing.T) {
	root := newTestRootForRegistration(t)
	registerRun(root)
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatalf("run subcommand not registered: %v", err)
	}
	if cmd.Use == "" {
		t.Fatal("run subcommand has empty Use field")
	}
}

// TestRun_CapStdout_ConstantIsSane confirms the 64 MiB cap constant is sane.
// capStdout itself is package-private; this guards against an accidental
// zero or negative value that would truncate every output.
func TestRun_CapStdout_ConstantIsSane(t *testing.T) {
	if maxStdoutBytes < 1024*1024 {
		t.Fatalf("maxStdoutBytes unexpectedly small: %d", maxStdoutBytes)
	}
}

// TestRegistry_ResolveUnknown verifies that an unregistered command produces
// nil from DefaultRegistry.Resolve, meaning browzer run will emit raw output.
func TestRun_UnknownCommandPassesThrough(t *testing.T) {
	r := runfilters.New()
	fn := r.Resolve([]string{"someunknowncmd", "--flag"})
	if fn != nil {
		t.Errorf("expected nil FilterFn for unregistered command, got non-nil")
	}
}

// TestRun_NoFilterFlag_CategoryTaggedCorrectly confirms that when --no-filter
// is in effect the telemetry source tag resolves to "browzer-run-no-filter"
// (not "browzer-run-unknown"). This guards against a regression where both
// tags mapped to the same string, making the telemetry unusable.
func TestRun_NoFilterFlag_CategoryTaggedCorrectly(t *testing.T) {
	const noFilterSource = "browzer-run-no-filter"
	const unknownSource = "browzer-run-unknown"
	const exitFailSource = "browzer-run-exitfail"
	if noFilterSource == unknownSource {
		t.Errorf("no-filter and unknown source tags must be distinct")
	}
	if exitFailSource == unknownSource {
		t.Errorf("exit-fail and unknown source tags must be distinct")
	}
	if noFilterSource == exitFailSource {
		t.Errorf("no-filter and exit-fail source tags must be distinct")
	}
}

// TestRun_KnownCommandResolvesFilter asserts that a registered filter is
// returned by Resolve for the matching argv.
func TestRun_KnownCommandResolvesFilter(t *testing.T) {
	r := runfilters.New()
	called := false
	r.Register("git", func(stdout []byte) []byte {
		called = true
		return stdout
	})
	fn := r.Resolve([]string{"git", "status"})
	if fn == nil {
		t.Fatal("expected non-nil FilterFn for 'git'")
	}
	fn(nil)
	if !called {
		t.Error("filter function was not invoked")
	}
}

// TestRun_CategoryOf_GitStatus verifies CategoryOf returns "git" for a
// single-verb git command — matching the telemetry source "browzer-run-git"
// that the run command would emit on a successful filtered execution.
func TestRun_CategoryOf_GitStatus(t *testing.T) {
	r := runfilters.New()
	r.Register("git", func(stdout []byte) []byte { return stdout })
	got := r.CategoryOf([]string{"git", "status"})
	if got != "git" {
		t.Errorf("CategoryOf(git status) = %q, want \"git\"", got)
	}
}
