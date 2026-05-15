package hooks

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// fakeRoot builds a minimal root command and registers the hook group on it,
// mirroring how root.go wires the real binary.
func fakeRoot() *cobra.Command {
	root := &cobra.Command{Use: "browzer"}
	RegisterHooks(root)
	return root
}

func TestRegisterHooks_CommandExists(t *testing.T) {
	root := fakeRoot()
	found, _, err := root.Find([]string{"hook"})
	if err != nil {
		t.Fatalf("unexpected error finding hook command: %v", err)
	}
	if found == nil || found.Name() != "hook" {
		t.Fatal("hook command not registered on root")
	}
}

func TestRegisterHooks_AllSubcommandsPresent(t *testing.T) {
	want := []string{
		"rewrite-bash", "rewrite-read", "prompt-guard",
		"postuse-run", "postuse-read", "postuse-grep", "postuse-glob",
		"session-start", "session-summary",
		"track-cli", "track-wasted",
		"subagent-stop", "block-glob", "suggest-grep",
		"contract", "init", "prd-frozen",
		"validate-subagent-type", "sync-on-push", "incremental-sync",
	}
	root := fakeRoot()
	hookCmd, _, _ := root.Find([]string{"hook"})
	if hookCmd == nil {
		t.Fatal("hook command missing")
	}

	subs := map[string]bool{}
	for _, c := range hookCmd.Commands() {
		subs[c.Name()] = true
	}

	for _, name := range want {
		if !subs[name] {
			t.Errorf("missing subcommand: %s", name)
		}
	}

	if len(want) != 20 {
		t.Errorf("test guard list has %d entries, expected 20", len(want))
	}
}

func TestRegisterHooks_HelpListsSubcommands(t *testing.T) {
	root := fakeRoot()
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"hook", "--help"})
	// cobra writes help to stdout; we capture it via SetOut.
	// Ignore the error — cobra returns nil for help.
	_ = root.Execute()
	help := out.String()
	for _, spec := range hookSpecs {
		if !strings.Contains(help, spec.name) {
			t.Errorf("help output missing subcommand %q", spec.name)
		}
	}
}
