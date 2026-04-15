// Package commands — `browzer plugin` group.
//
// Installs the Browzer Claude Code plugin (hooks + skills + agents) into
// `.claude/plugins/browzer/` (project-local, default) or
// `~/.claude/plugins/browzer/` (user-level, --global).
//
// Source resolution tries monorepo-detect first (walks up looking for
// `packages/skills/.claude-plugin/plugin.json`) and falls back to a
// user-supplied `--from <path>`. v1 does not download release tarballs
// — users who don't run the CLI from a monorepo clone need to point
// --from at their own checkout. Release-tarball download is tracked
// for a follow-up once the CLI ships as a binary.
package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

// pluginDirName is the single folder created under `.claude/plugins/` or
// `~/.claude/plugins/`. Keeping it as a package-level constant lets both
// install and uninstall agree on the path.
const pluginDirName = "browzer"

// pluginCopyEntries is the set of relative paths inside the source
// `packages/skills/` tree that get copied into the installed plugin.
// Anything not listed here stays out of the install (tests, package.json,
// README, evals fixtures, …).
var pluginCopyEntries = []string{
	".claude-plugin/plugin.json",
	".claude-plugin/marketplace.json",
	"hooks/hooks.json",
	"hooks/guards",
	"agents",
	"rag",
	"workflow",
	"ops",
	"tools",
}

// pluginSkipSuffixes is the blocklist applied while copying pluginCopyEntries
// — test files and evals fixtures must not ship in the installed plugin.
var pluginSkipSuffixes = []string{
	".test.mjs",
	".test.ts",
	"/evals",
}

func registerPlugin(parent *cobra.Command) {
	g := &cobra.Command{
		Use:   "plugin",
		Short: "Manage the Browzer Claude Code plugin",
		Long: `Manage the Browzer Claude Code plugin.

The plugin bundles the Browzer hooks (Read auto-rewrite, Glob block,
Grep suggest, Bash rewrite), skills (RAG + workflow + ops + tools), and
agents into a Claude Code plugin folder. Installing it lets Claude Code
pick up these capabilities in the next session automatically.

Default install location is project-local (` + "`" + `.claude/plugins/browzer/` + "`" + `
in the current git root). Use --global for a user-level install
(` + "`" + `~/.claude/plugins/browzer/` + "`" + `).

Examples:
  browzer plugin install                    # project-local
  browzer plugin install --global           # user-level
  browzer plugin install --from /path       # override source dir
  browzer plugin update                     # alias for install (idempotent)
  browzer plugin uninstall                  # remove project-local install
  browzer plugin uninstall --global         # remove user-level install
`,
	}
	g.AddCommand(newPluginInstallCommand("install"))
	g.AddCommand(newPluginInstallCommand("update")) // idempotent alias
	g.AddCommand(newPluginUninstallCommand())
	parent.AddCommand(g)
}

// newPluginInstallCommand returns the `install` command, parameterized
// on the display name so `plugin update` shares the same code path.
func newPluginInstallCommand(use string) *cobra.Command {
	var (
		fromFlag   string
		globalFlag bool
		quietFlag  bool
	)
	cmd := &cobra.Command{
		Use:   use,
		Short: "Install or update the Browzer Claude Code plugin (idempotent)",
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := resolvePluginSource(fromFlag)
			if err != nil {
				return err
			}
			dst, err := resolvePluginTarget(globalFlag)
			if err != nil {
				return err
			}
			if err := installPlugin(src, dst); err != nil {
				return err
			}
			if !globalFlag {
				if gitRoot, _ := filepath.Abs(filepath.Dir(filepath.Dir(filepath.Dir(dst)))); gitRoot != "" {
					if addErr := ensureClaudePluginsIgnored(gitRoot); addErr != nil && !quietFlag {
						ui.Warn(fmt.Sprintf("could not update .gitignore: %v", addErr))
					}
				}
			}
			if !quietFlag {
				scope := "project"
				if globalFlag {
					scope = "global"
				}
				ui.Success(fmt.Sprintf("Browzer plugin installed (%s): %s", scope, dst))
				fmt.Fprintln(cmd.OutOrStdout(), "Restart Claude Code (or start a new session) to pick it up.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromFlag, "from", "", "Override the source dir (defaults to monorepo-detect)")
	cmd.Flags().BoolVar(&globalFlag, "global", false, "Install to ~/.claude/plugins/browzer/ instead of ./.claude/plugins/browzer/")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "Suppress the success output")
	return cmd
}

func newPluginUninstallCommand() *cobra.Command {
	var globalFlag bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Browzer Claude Code plugin",
		RunE: func(cmd *cobra.Command, args []string) error {
			dst, err := resolvePluginTarget(globalFlag)
			if err != nil {
				return err
			}
			if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
				ui.Info(fmt.Sprintf("Plugin not installed at %s — nothing to do.", dst))
				return nil
			}
			if err := os.RemoveAll(dst); err != nil {
				return fmt.Errorf("remove %s: %w", dst, err)
			}
			ui.Success(fmt.Sprintf("Browzer plugin removed: %s", dst))
			return nil
		},
	}
	cmd.Flags().BoolVar(&globalFlag, "global", false, "Remove from ~/.claude/plugins/browzer/ instead of ./.claude/plugins/browzer/")
	return cmd
}

// resolvePluginSource returns the absolute path to the source
// `packages/skills/` tree. When --from is set the function validates the
// shape (must contain `.claude-plugin/plugin.json`); otherwise it walks up
// from CWD looking for that marker.
func resolvePluginSource(from string) (string, error) {
	if from != "" {
		abs, err := filepath.Abs(from)
		if err != nil {
			return "", fmt.Errorf("resolve --from: %w", err)
		}
		if err := validatePluginSource(abs); err != nil {
			return "", err
		}
		return abs, nil
	}
	// Monorepo-detect: walk up from CWD looking for packages/skills.
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root := findPackagesSkills(cwd); root != "" {
		return root, nil
	}
	// Fallback hint: the most common install vector is a monorepo clone,
	// so the error tells the user exactly what to do.
	return "", cliErrors.New(
		"could not find `packages/skills/.claude-plugin/plugin.json` walking up from CWD.\n" +
			"Run from a Browzer monorepo clone, or pass --from <path/to/packages/skills>.",
	)
}

func findPackagesSkills(start string) string {
	dir := start
	for i := 0; i < 32; i++ {
		candidate := filepath.Join(dir, "packages", "skills")
		if validatePluginSource(candidate) == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

func validatePluginSource(dir string) error {
	manifest := filepath.Join(dir, ".claude-plugin", "plugin.json")
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf("plugin source %q missing .claude-plugin/plugin.json", dir)
	}
	return nil
}

// resolvePluginTarget returns the install directory. --global picks
// ~/.claude/plugins/browzer/; project-local picks <gitRoot>/.claude/plugins/browzer/.
func resolvePluginTarget(global bool) (string, error) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "plugins", pluginDirName), nil
	}
	gitRoot := git.FindGitRoot("")
	if gitRoot == "" {
		return "", cliErrors.New(
			"project-local install requires a git repository.\n" +
				"Run this from inside a git-tracked project, or pass --global.",
		)
	}
	return filepath.Join(gitRoot, ".claude", "plugins", pluginDirName), nil
}

// installPlugin copies pluginCopyEntries from src into dst. The target
// directory is wiped first so the install is idempotent (no stale files
// left behind after entries are renamed or removed upstream).
func installPlugin(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("wipe target: %w", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	for _, entry := range pluginCopyEntries {
		srcPath := filepath.Join(src, entry)
		dstPath := filepath.Join(dst, entry)
		info, err := os.Stat(srcPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Optional entries (e.g. marketplace.json) may be missing.
				continue
			}
			return fmt.Errorf("stat %s: %w", srcPath, err)
		}
		if info.IsDir() {
			if err := copyTree(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
				return err
			}
			if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyTree(srcRoot, dstRoot string) error {
	return filepath.Walk(srcRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcRoot, p)
		if shouldSkipPluginPath(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(dstRoot, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode())
		}
		return copyFile(p, dst, info.Mode())
	})
}

func shouldSkipPluginPath(rel string) bool {
	// Normalize on forward slashes so the Windows path separator doesn't
	// make `/evals` checks miss.
	norm := filepath.ToSlash(rel)
	for _, suf := range pluginSkipSuffixes {
		if strings.HasSuffix(norm, suf) || strings.Contains(norm, suf+"/") {
			return true
		}
	}
	return false
}

func copyFile(src, dst string, mode os.FileMode) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	d, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, s)
	return err
}

// ensureClaudePluginsIgnored appends `.claude/plugins/` to <gitRoot>/.gitignore
// when the entry is missing. Idempotent; safe to call on every install.
// We ignore the parent directory (plugins/) rather than the specific browzer/
// subdir so future plugins install without new diff noise.
func ensureClaudePluginsIgnored(gitRoot string) error {
	const entry = ".claude/plugins/"
	path := filepath.Join(gitRoot, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	out := append(data, []byte(prefix+entry+"\n")...)
	return os.WriteFile(path, out, 0o644)
}
