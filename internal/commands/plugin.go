// Package commands — `browzer plugin` group.
//
// Prints install / uninstall instructions for the Browzer Claude Code
// plugin. Claude Code only loads plugins via two official paths —
// `/plugin marketplace add` + `/plugin install` inside the IDE, or the
// `claude --plugin-dir <path>` flag for local dev. There is no supported
// filesystem-drop install, so this command is a printer, not a copier.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	g.AddCommand(newPluginDoctorCommand(os.Getwd))
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

// newPluginDoctorCommand returns the `browzer plugin doctor` subcommand.
// getCwdFn is injected so tests can substitute a controlled working directory.
//
// Exit-code contract (aligns with the CLI-wide convention):
//   - WARN: lines are soft signals; they do NOT set the failure flag and do
//     NOT change the exit code. A run with only WARN lines exits 0.
//   - FAIL: lines are hard failures; they set anyFail=true and cause exit 1.
func newPluginDoctorCommand(getCwdFn func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that the Browzer plugin is correctly installed and enabled",
		Long: `Runs three health checks and prints one PASS:/WARN:/FAIL: line per check.

Exits 0 when all checks pass or only WARN lines are emitted.
Exits 1 when any FAIL check is encountered.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := getCwdFn()
			if err != nil {
				cwd = ""
			}
			w := cmd.OutOrStdout()
			anyFail := false

			// Check 1: plugin installed.
			if checkPluginInstalled() {
				_, _ = fmt.Fprintln(w, "PASS: plugin installed (~/.claude/plugins/)")
			} else {
				_, _ = fmt.Fprintln(w, "FAIL: plugin not found in ~/.claude/plugins/ — run: /plugin marketplace add browzer@browzer-marketplace")
				anyFail = true
			}

			// Check 2: ~/.claude.json parseable.
			claudeJSON, parseOK, parsedMsg := readClaudeJSON()
			if !parseOK {
				_, _ = fmt.Fprintln(w, "WARN: "+parsedMsg)
			} else {
				_, _ = fmt.Fprintln(w, "PASS: ~/.claude.json is parseable")
			}

			// Check 3: enabledPlugins populated for CWD.
			if parseOK && claudeJSON != nil {
				ok, msg := checkEnabledPlugins(claudeJSON, cwd)
				if ok {
					_, _ = fmt.Fprintln(w, "PASS: "+msg)
				} else {
					_, _ = fmt.Fprintln(w, "FAIL: "+msg)
					anyFail = true
				}
			} else if parseOK {
				_, _ = fmt.Fprintln(w, "WARN: enabledPlugins check skipped (empty config)")
			}

			if anyFail {
				return fmt.Errorf("one or more checks failed")
			}
			return nil
		},
	}
}

// checkPluginInstalled returns true when the plugin appears to be installed:
// either ~/.claude/plugins/installed_plugins.json contains a key with "browzer"
// (case-insensitive substring, to handle composite keys such as
// "browzer@browzer-marketplace"), or the cache directory
// ~/.claude/plugins/cache/browzer-marketplace/browzer/ exists.
//
// Filesystem access (os.UserHomeDir, os.ReadFile, os.Stat) is not injected
// because tests control the home directory via t.Setenv("HOME", ...) and
// use t.TempDir()-based paths — the standard Go convention for home-relative
// filesystem tests. Only getCwdFn is injected on newPluginDoctorCommand
// because cwd cannot be overridden via an environment variable.
func checkPluginInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	// Path A: installed_plugins.json contains a key whose lowercase form
	// contains "browzer". Matches both bare "browzer" and composite keys
	// such as "browzer@browzer-marketplace".
	installedPath := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	if data, err := os.ReadFile(installedPath); err == nil {
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			for k := range m {
				if strings.Contains(strings.ToLower(k), "browzer") {
					return true
				}
			}
		}
	}
	// Path B: cache directory exists.
	cacheDir := filepath.Join(home, ".claude", "plugins", "cache", "browzer-marketplace", "browzer")
	if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
		return true
	}
	return false
}

// claudeJSONShape is the subset of ~/.claude.json that doctor reads.
type claudeJSONShape struct {
	Projects map[string]claudeProjectShape `json:"projects"`
}

type claudeProjectShape struct {
	EnabledPlugins map[string]any `json:"enabledPlugins"`
}

// readClaudeJSON parses ~/.claude.json defensively. Returns (parsed, ok, message).
// When the file is absent or JSON decode fails, ok=false and message describes the issue.
func readClaudeJSON() (*claudeJSONShape, bool, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false, "cannot determine home directory: " + err.Error()
	}
	path := filepath.Join(home, ".claude.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, "~/.claude.json not found (Claude Code may not have been run yet)"
		}
		return nil, false, "cannot read ~/.claude.json: " + err.Error()
	}
	var cfg claudeJSONShape
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, "~/.claude.json exists but JSON decode failed: " + err.Error()
	}
	return &cfg, true, "ok"
}

// checkEnabledPlugins inspects projects[cwd].enabledPlugins in the parsed
// ~/.claude.json. Returns (true, message) when a key containing "browzer"
// (case-insensitive) is found; (false, message) otherwise.
func checkEnabledPlugins(cfg *claudeJSONShape, cwd string) (bool, string) {
	if cfg.Projects == nil {
		return false, "enabledPlugins not set for this project — run /plugin install browzer@browzer-marketplace inside Claude Code"
	}
	proj, ok := cfg.Projects[cwd]
	if !ok {
		return false, "no project entry for current directory in ~/.claude.json — open Claude Code in this directory and install the plugin"
	}
	if len(proj.EnabledPlugins) == 0 {
		return false, "enabledPlugins is empty for this project — run /plugin install browzer@browzer-marketplace inside Claude Code"
	}
	for k := range proj.EnabledPlugins {
		if strings.Contains(strings.ToLower(k), "browzer") {
			return true, "browzer plugin is enabled for this project"
		}
	}
	return false, "enabledPlugins exists but no browzer key found — run /plugin install browzer@browzer-marketplace inside Claude Code"
}
