// Package commands — `browzer plugin` group.
//
// Prints install / uninstall instructions for the Browzer Claude Code
// plugin. Claude Code only loads plugins via two official paths —
// `/plugin marketplace add` + `/plugin install` inside the IDE, or the
// `claude --plugin-dir <path>` flag for local dev. There is no supported
// filesystem-drop install, so this command is a printer, not a copier.
package commands

import (
	"fmt"

	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

// pluginMarketplaceRepo is the public repo mirrored from
// `packages/skills/` in the monorepo (see `.github/workflows/mirror-skills.yml`).
const pluginMarketplaceRepo = "browzeremb/skills"

func registerPlugin(parent *cobra.Command) {
	g := &cobra.Command{
		Use:   "plugin",
		Short: "Install Browzer Claude Code plugin (recommended)",
		Long: `Show how to install the Browzer Claude Code plugin.

The plugin is HIGHLY RECOMMENDED — without it, the CLI works but you
lose the integrations that make it shine in Claude Code:

  - Read / Glob / Grep auto-rewrite through the token-saving daemon
  - SessionStart hook that auto-starts browzer-daemon + registers the
    active model with the tracker
  - Workflow skills (prd → task → execute → commit → sync) and the
    browzer orchestrator agent
  - Pre-flight context probe (browzer status --json) every session
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			printPluginInstructions(cmd)
			return nil
		},
	}
	g.AddCommand(newPluginInstallCommand())
	g.AddCommand(newPluginUninstallCommand())
	parent.AddCommand(g)
}

func newPluginInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Print instructions to install the plugin via Claude Code's marketplace",
		RunE: func(cmd *cobra.Command, args []string) error {
			printPluginInstructions(cmd)
			return nil
		},
	}
}

func newPluginUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Print instructions to remove the plugin via Claude Code",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(w, "Remove the plugin from inside Claude Code:")
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, "  /plugin uninstall browzer@browzer-marketplace")
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, "Or, if installed via --plugin-dir, just stop passing the flag.")
			return nil
		},
	}
}

// printPluginInstructions writes the recommended-install banner. Called
// by `browzer plugin`, `browzer plugin install`, and `browzer init` so
// every surface says the same thing.
func printPluginInstructions(cmd *cobra.Command) {
	w := cmd.OutOrStdout()
	ui.Warn("The Browzer Claude Code plugin is HIGHLY RECOMMENDED.")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Without it the CLI still works, but you lose:")
	_, _ = fmt.Fprintln(w, "  - token-saving Read/Glob/Grep auto-rewrite via the daemon")
	_, _ = fmt.Fprintln(w, "  - the SessionStart hook that boots browzer-daemon")
	_, _ = fmt.Fprintln(w, "  - workflow skills (prd → task → execute → commit → sync)")
	_, _ = fmt.Fprintln(w, "  - pre-flight context probe on every Claude Code session")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Install it — run these INSIDE Claude Code:")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "  /plugin marketplace add %s\n", pluginMarketplaceRepo)
	_, _ = fmt.Fprintln(w, "  /plugin install browzer@browzer-marketplace")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Local dev (uncommitted changes in a monorepo clone):")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "  claude --plugin-dir ./packages/skills")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Docs: https://github.com/%s\n", pluginMarketplaceRepo)
}
