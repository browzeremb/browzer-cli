package commands

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestDualRegistration_LegacyAndNounGroupedShareHandler asserts that
// every dual-registered command exists under both the top-level
// (legacy) tree AND the noun-grouped `workspace` subtree, and that
// both incarnations expose the same flag set. The two cobra.Command
// objects are distinct (each registerX call builds a fresh closure),
// so we compare what's externally observable: command name, short
// description, and the names of every flag.
func TestDualRegistration_LegacyAndNounGroupedShareHandler(t *testing.T) {
	root := NewRootCommand("test")

	// Find the workspace subgroup.
	var ws *cobra.Command
	for _, c := range root.Commands() {
		if c.Use == "workspace" {
			ws = c
			break
		}
	}
	if ws == nil {
		t.Fatal("workspace subcommand group not registered")
	}

	dual := []string{"init", "index", "status", "explore", "search"}
	for _, name := range dual {
		legacy := findChild(root, name)
		grouped := findChild(ws, name)
		if legacy == nil {
			t.Errorf("legacy `browzer %s` not registered", name)
			continue
		}
		if grouped == nil {
			t.Errorf("noun-grouped `browzer workspace %s` not registered", name)
			continue
		}
		if legacy.Short != grouped.Short {
			t.Errorf("%s: Short drift between legacy and grouped (%q vs %q)", name, legacy.Short, grouped.Short)
		}
		legacyFlags := flagNames(legacy)
		groupedFlags := flagNames(grouped)
		if !sameSet(legacyFlags, groupedFlags) {
			t.Errorf("%s: flag set drift\n  legacy:  %v\n  grouped: %v", name, legacyFlags, groupedFlags)
		}
	}
}

func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func flagNames(c *cobra.Command) []string {
	var out []string
	c.Flags().VisitAll(func(f *pflag.Flag) {
		out = append(out, f.Name)
	})
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]struct{}, len(a))
	for _, s := range a {
		m[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := m[s]; !ok {
			return false
		}
	}
	return true
}

// TestInternalWorkflowPackageRemoved pins that the internal/workflow
// directory no longer exists in the source tree.
func TestInternalWorkflowPackageRemoved(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	workflowDir := filepath.Join(repoRoot, "packages", "cli", "internal", "workflow")

	_, err := os.Stat(workflowDir)
	if err == nil {
		t.Fatalf("expected internal/workflow directory to be absent, but os.Stat(%q) succeeded", workflowDir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) returned unexpected error: %v", workflowDir, err)
	}
}

// TestSchemasDirectoryRemoved pins that the packages/cli/schemas/
// directory (CUE schema sources, generated artifacts, and fixtures)
// no longer exists. It was retired together with workflow.json once
// the markdown-chains pipeline became the only contract.
func TestSchemasDirectoryRemoved(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	schemasDir := filepath.Join(repoRoot, "packages", "cli", "schemas")

	_, err := os.Stat(schemasDir)
	if err == nil {
		t.Fatalf("expected packages/cli/schemas directory to be absent, but os.Stat(%q) succeeded", schemasDir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) returned unexpected error: %v", schemasDir, err)
	}
}

// makefileSchemasCouplingRE matches any re-introduction of the deleted
// schemas/ codegen path, cuelang dependency, or go-mutesting in the
// Makefile. Each alternative covers the full surface of that coupling
// class — adding a new cuelang module or a new schemas/ target is
// caught without updating the list.
var makefileSchemasCouplingRE = regexp.MustCompile(
	`cuelang\.org/go|make\s+-C\s+schemas|schemas/|go-mutesting`,
)

// ciLocalSchemasCouplingRE mirrors makefileSchemasCouplingRE for the
// ci-local.sh script, which carries distinct coupling patterns (module
// path + versioned cue tag).
var ciLocalSchemasCouplingRE = regexp.MustCompile(
	`cuelang\.org/go|make\s+-C\s+schemas|cue\s+v0\.`,
)

// TestBuildSurfaceFreeOfSchemasCoupling pins that the host Makefile and
// ci-local.sh no longer chain through the deleted schemas/ codegen or
// auto-install cuelang. Smoke-level regression against accidental
// re-introduction of either coupling.
func TestBuildSurfaceFreeOfSchemasCoupling(t *testing.T) {
	repoRoot := testutil.RepoRoot(t)
	cases := []struct {
		path string
		re   *regexp.Regexp
	}{
		{
			path: filepath.Join(repoRoot, "packages", "cli", "Makefile"),
			re:   makefileSchemasCouplingRE,
		},
		{
			path: filepath.Join(repoRoot, "packages", "cli", "scripts", "ci-local.sh"),
			re:   ciLocalSchemasCouplingRE,
		},
	}
	for _, tc := range cases {
		body, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if loc := tc.re.FindString(string(body)); loc != "" {
			t.Errorf("%s still contains forbidden pattern matched by %s: %q", tc.path, tc.re, loc)
		}
	}
}

func TestVerbose_FlagCountIncrements(t *testing.T) {
	output.Verbose = 0
	root := NewRootCommand("test")
	root.SetArgs([]string{"-vv", "--help"})
	_ = root.Execute()
	if output.Verbose != 2 {
		t.Fatalf("Verbose = %d, want 2", output.Verbose)
	}
}
