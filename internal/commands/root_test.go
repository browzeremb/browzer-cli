package commands

import (
	"testing"

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
