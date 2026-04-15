// Package commands wires every cobra command into the root tree.
//
// Each command lives in its own file (login.go, init.go, sync.go, ...)
// mirroring the legacy src/commands/*.ts layout. NewRootCommand is the
// single entrypoint called from cmd/browzer/main.go.
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/spf13/cobra"
)

// Ultra is the package-level flag for --ultra (compact output across
// read/explore/search/deps). Set by NewRootCommand's PersistentPreRunE.
var Ultra bool

// NewRootCommand returns the configured cobra root command. version is
// injected from main via -ldflags so the published binary reports its
// own release.
func NewRootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "browzer",
		Short:         "Browzer CLI — hybrid RAG for your codebase",
		Long:          "Browzer CLI — hybrid vector + Graph RAG for your codebase. Run `browzer login` to start.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Verbosity ladder: -v/-vv/-vvv increases output.Verbose (0-3).
	root.PersistentFlags().CountVarP(&output.Verbose, "verbose", "v", "increase verbosity (-v decisions, -vv subprocess, -vvv raw I/O)")

	// Global --ultra flag: compact output across read/explore/search/deps.
	root.PersistentFlags().BoolVar(&Ultra, "ultra", false, "ultra-compact output (smaller payloads, fewer fields)")

	// Global --llm flag: suppresses banners, disables colors, no spinners.
	// Also honored via BROWZER_LLM env so shell wrappers (e.g. Claude
	// SKILL runners) can opt-in once per session. We set NO_COLOR too so
	// any third-party lib honoring the convention degrades as well.
	root.PersistentFlags().Bool("llm", false, "LLM mode: suppress banner, disable colors, no spinners")

	// Pre-scan os.Args + BROWZER_LLM so --help/--version (which bypass
	// cobra's PersistentPreRunE) still see LLMMode. PersistentPreRunE
	// below handles the normal command path.
	applyLLMMode := func(llm bool) {
		if llm {
			ui.LLMMode = true
			_ = os.Setenv("NO_COLOR", "1")
		}
	}
	if envLLMEnabled() {
		applyLLMMode(true)
	} else {
		for _, a := range os.Args[1:] {
			if a == "--llm" || a == "--llm=true" || a == "--llm=1" {
				applyLLMMode(true)
				break
			}
		}
	}
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		llm, _ := cmd.Flags().GetBool("llm")
		if envLLMEnabled() {
			llm = true
		}
		applyLLMMode(llm)
		return nil
	}

	// Top-level: legacy aliases retained for backward compat. The
	// canonical noun-grouped form lives under `browzer workspace ...`.
	registerLogin(root)
	registerLogout(root)
	registerInit(root)
	registerStatus(root)
	registerWorkspaceIndex(root) // `browzer index` top-level alias
	registerWorkspaceSync(root)  // `browzer sync` top-level alias
	registerExplore(root)
	registerSearch(root)
	registerAsk(root)
	registerDeps(root)
	registerJob(root)
	registerUpgrade(root)
	registerRead(root)
	registerDaemon(root)
	registerConfig(root)
	registerGain(root)

	// `org` subcommand group.
	registerOrg(root)

	// `workspace` subcommand group + canonical noun-grouped re-registration.
	ws := registerWorkspace(root)
	registerInit(ws)
	registerStatus(ws)
	registerWorkspaceIndex(ws)
	registerWorkspaceDocs(ws)
	registerExplore(ws)
	registerSearch(ws)
	registerDeps(ws)

	// Register `{{heading ...}}` as a template function so both the
	// help and usage templates can colorize section labels without
	// touching each command's Long/Short text. When color is off the
	// function is the identity, so piped output stays plain ASCII.
	cobra.AddTemplateFunc("heading", ui.Heading)

	// Colorized help/usage template — same structure cobra ships with
	// upstream (kept field-for-field), but with `{{heading ...}}` on
	// every section title. Changes in cobra's default template would
	// need a sync here; the payoff is a one-shot palette update.
	colorizedHelp := `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`
	colorizedUsage := `{{heading "Usage:"}}{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

{{heading "Aliases:"}}
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{heading "Examples:"}}
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

{{heading "Available Commands:"}}{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{heading $group.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

{{heading "Additional Commands:"}}{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{heading "Flags:"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{heading "Global Flags:"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

{{heading "Additional help topics:"}}{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
	root.SetUsageTemplate(colorizedUsage)
	root.SetHelpTemplate(colorizedHelp + "\n" + agentTips + output.ExitCodesHelp + "\n")

	// Version string: brand banner + plain "<command> <version>".
	// Register `banner` as a template func so LLMMode (set by
	// PersistentPreRunE) is evaluated at render time, not at wiring
	// time — otherwise `--llm --version` would still print the banner.
	cobra.AddTemplateFunc("banner", func() string {
		if ui.LLMMode {
			return ""
		}
		return ui.Banner(version) + "\n"
	})
	root.SetVersionTemplate(`{{banner}}browzer {{.Version}}` + "\n")

	// Prepend the brand banner on the ROOT help screen only. We wrap
	// the default HelpFunc instead of baking color into SetHelpTemplate
	// because the template is a plain Go template — it can't call
	// term.IsTerminal, so ANSI would leak into piped output.
	// Subcommand help stays clean (no banner) so `browzer init --help`
	// reads like a proper man page.
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root && !ui.LLMMode {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version))
		}
		defaultHelp(cmd, args)
	})

	return root
}

// envLLMEnabled reports whether BROWZER_LLM requests LLM mode. Presence
// alone is NOT enough — we parse the value so users can set
// `BROWZER_LLM=0` (or `false`/`off`/empty) to explicitly disable,
// unlike NO_COLOR where presence is the signal. The truthy set matches
// GNU-ish conventions: 1, true, yes, on (case-insensitive).
func envLLMEnabled() bool {
	v, ok := os.LookupEnv("BROWZER_LLM")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

const agentTips = `Agent-friendly tips:
  • Canonical form is noun-grouped: ` + "`browzer workspace {init,index,docs,status,explore,search}`" + `.
    ` + "`browzer index`" + ` is a top-level alias for ` + "`browzer workspace index`" + `.
  • Every read/search command supports --json and --save <file>.
  • Combine --save with --json to write a clean JSON document
    without banners polluting stdout (ideal for Claude SKILLs).
  • ` + "`browzer explore --schema`" + ` / ` + "`browzer deps --schema`" + ` discovers the response shape.
  • ` + "`browzer workspace get <id> --save ws.json`" + ` discovers the workspace shape.
  • ` + "`browzer workspace sync`" + ` (alias: ` + "`browzer sync`" + `) re-indexes both code and docs non-interactively.
  • ` + "`browzer workspace index`" + ` re-parses code only; ` + "`browzer workspace docs`" + ` interactively re-indexes documents.
  • ` + "`browzer login --key $BROWZER_API_KEY`" + ` for non-interactive login.
`
