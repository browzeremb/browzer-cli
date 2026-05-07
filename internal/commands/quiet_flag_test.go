package commands

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// TestQuietFlag_RegisteredOnAllReadVerbs pins the cross-cutting --quiet
// contract from RETRO §16 #1. Every top-level read verb a parallel
// agent batch may invoke MUST accept --quiet — even when there is
// nothing to silence today — so a single rejection cannot cancel
// sibling calls. Adding a new top-level read verb without --quiet is
// a regression; this test catches it at registration time.
func TestQuietFlag_RegisteredOnAllReadVerbs(t *testing.T) {
	verbs := []struct {
		name     string
		register func(parent *cobra.Command)
	}{
		{"status", registerStatus},
		{"explore", registerExplore},
		{"search", registerSearch},
		{"deps", registerDeps},
		{"mentions", registerMentions},
		{"sync", registerWorkspaceSync},
	}

	for _, v := range verbs {
		v := v
		t.Run(v.name, func(t *testing.T) {
			root := newTestRootForRegistration(t)
			v.register(root)
			cmd, _, err := root.Find([]string{v.name})
			if err != nil {
				t.Fatalf("%s subcommand not registered: %v", v.name, err)
			}
			if f := cmd.Flags().Lookup("quiet"); f == nil {
				t.Fatalf("%s is missing --quiet flag (RETRO §16 #1)", v.name)
			}
		})
	}
}

// TestQuietFlag_AcceptedByCobraParser verifies that `--quiet` does NOT
// trigger cobra's `unknown flag` rejection on any of the cross-cutting
// verbs. We exercise the parse layer only — no business logic — by
// stubbing RunE to a no-op and feeding `--quiet` through SetArgs.
//
// Without this fix, `browzer status --quiet` (and the other verbs)
// exited 1 with `Error: unknown flag: --quiet`, which cancels parallel
// agent batches mixing the new verbs with `workflow get-step --quiet`.
func TestQuietFlag_AcceptedByCobraParser(t *testing.T) {
	verbs := []struct {
		name     string
		register func(parent *cobra.Command)
		extra    []string // extra args some verbs require (e.g. <path>)
	}{
		{"status", registerStatus, nil},
		{"deps", registerDeps, []string{"some/file.go"}},
		{"mentions", registerMentions, []string{"some/file.go"}},
		// F-17: extend coverage to the verbs that adopted RegisterQuietFlag
		// later (sync via the workspace tree, explore + search after F-3).
		// sync runs with --dry-run --skip-code --skip-docs to keep the
		// no-op RunE path fast and avoid network/auth.
		{"explore", registerExplore, []string{"some-query"}},
		{"search", registerSearch, []string{"some-query"}},
		{"sync", registerWorkspaceSync, []string{"--dry-run", "--skip-code", "--skip-docs"}},
	}

	for _, v := range verbs {
		v := v
		t.Run(v.name, func(t *testing.T) {
			root := &cobra.Command{
				Use:           "browzer",
				SilenceErrors: true,
				SilenceUsage:  true,
			}
			v.register(root)

			// Replace RunE on the registered subcommand with a no-op
			// so we exercise only the flag-parse layer (the actual
			// verbs would otherwise hit auth + git + network).
			sub, _, err := root.Find([]string{v.name})
			if err != nil {
				t.Fatalf("subcommand not registered: %v", err)
			}
			sub.RunE = func(cmd *cobra.Command, args []string) error { return nil }
			sub.PersistentPreRunE = nil
			sub.PreRunE = nil

			args := append([]string{v.name, "--quiet"}, v.extra...)
			root.SetArgs(args)
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)

			if err := root.Execute(); err != nil {
				t.Fatalf("--quiet rejected by parser: %v\nstderr: %s", err, stderr.String())
			}
		})
	}
}
