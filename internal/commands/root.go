// Package commands wires every cobra command into the root tree.
//
// Each command lives in its own file (login.go, init.go, sync.go, ...)
// mirroring the legacy src/commands/*.ts layout. NewRootCommand is the
// single entrypoint called from cmd/browzer/main.go.
package commands

import (
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

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

	// Top-level: legacy aliases retained for backward compat. The
	// canonical noun-grouped form lives under `browzer workspace ...`.
	registerLogin(root)
	registerLogout(root)
	registerInit(root)
	registerStatus(root)
	registerSync(root)
	registerExplore(root)
	registerSearch(root)
	registerJob(root)

	// `workspace` subcommand group + canonical noun-grouped re-registration.
	ws := registerWorkspace(root)
	registerInit(ws)
	registerStatus(ws)
	registerSync(ws)
	registerExplore(ws)
	registerSearch(ws)

	root.SetHelpTemplate(root.HelpTemplate() + "\n" + agentTips + output.ExitCodesHelp + "\n")
	return root
}

const agentTips = `Agent-friendly tips:
  • Canonical form is noun-grouped: ` + "`browzer workspace {init,sync,status,explore,search}`" + `.
    Top-level aliases (` + "`browzer init`, `browzer sync`, ..." + `) still work for compat.
  • Every read/search command supports --json and --save <file>.
  • Combine --save with --json to write a clean JSON document
    without banners polluting stdout (ideal for Claude SKILLs).
  • ` + "`browzer explore --schema`" + ` discovers the response shape.
  • ` + "`browzer workspace get <id> --save ws.json`" + ` discovers the workspace shape.
  • ` + "`browzer sync --no-wait --json`" + ` + ` + "`browzer job get <id> --json`" + ` for async polling.
  • ` + "`browzer login --key $BROWZER_API_KEY`" + ` for non-interactive login.
`
