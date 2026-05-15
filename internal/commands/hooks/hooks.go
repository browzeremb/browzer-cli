// Package hooks wires the `browzer hook` cobra subcommand group.
// Each subcommand corresponds to a Claude Code hook guard.  Individual
// guard implementations live in sibling .go files and replace the stub
// RunE provided here.
package hooks

import (
	"fmt"
	"os"

	"github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// RegisterHooks attaches the `hook` command group to parent, following
// the same signature and call-site idiom as registerConfig and
// registerDaemon in the parent package.
func RegisterHooks(parent *cobra.Command) {
	parent.AddCommand(newHookCommand())
}

// hookSpec bundles a guard's command name, short description, and optional
// factory function.  When factory is nil the guard falls back to stubRunE.
// Adding a new guard requires a single entry here — no switch arm needed.
type hookSpec struct {
	name    string
	short   string
	factory func() *cobra.Command
}

// hookSpecs is the single source of truth for every Claude Code guard
// subcommand in the hook group.  Order determines help output order.
var hookSpecs = []hookSpec{
	{"rewrite-bash", "Rewrite bash commands before execution", newRewriteBashCommand},
	{"rewrite-read", "Rewrite file-read paths before execution", newRewriteReadCommand},
	{"prompt-guard", "Inspect and optionally block user prompts", newPromptGuardCommand},
	{"postuse-run", "Post-tool-use guard for Bash/Run calls", newPostuseRunCommand},
	{"postuse-read", "Post-tool-use guard for Read calls", newPostuseReadCommand},
	{"postuse-grep", "Post-tool-use guard for Grep calls", newPostuseGrepCommand},
	{"postuse-glob", "Post-tool-use guard for Glob calls", newPostuseGlobCommand},
	{"session-start", "Runs at the start of a Claude Code session", newSessionStartCommand},
	{"session-summary", "Runs when a session summary is produced", newSessionSummaryCommand},
	{"track-cli", "Track CLI invocations for telemetry", newTrackCLICommand},
	{"track-wasted", "Track wasted / redundant tool calls", newTrackWastedCommand},
	{"subagent-stop", "Guard fired when a subagent stops", newSubagentStopCommand},
	{"block-glob", "Block glob patterns that match sensitive paths", newBlockGlobCommand},
	{"suggest-grep", "Suggest grep alternatives when glob is wasteful", newSuggestGrepCommand},
	{"contract", "Enforce plugin hook contracts", newContractCommand},
	{"init", "Guard fired at workspace init time", newInitGuardCommand},
	{"prd-frozen", "Prevent edits to frozen PRD documents", newPrdFrozenCommand},
	{"validate-subagent-type", "Validate the subagent type field in dispatches", newValidateSubagentTypeCommand},
	{"sync-on-push", "Trigger workspace sync on git push", newSyncOnPushCommand},
	{"incremental-sync", "Incremental workspace sync guard", newIncrementalSyncCommand},
}

// stubRunE is the shared RunE for every guard that has not yet been
// implemented.  It writes a short message to stderr and exits with
// ExitPassthrough so Claude Code keeps its default behaviour.
func stubRunE(name string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		_, _ = fmt.Fprintf(os.Stderr, "browzer hook %s: not yet implemented\n", name)
		_, _ = os.Exit, hooks.ExitPassthrough
		return nil // unreachable — keeps the compiler happy
	}
}

func newHookCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook <name>",
		Short: "Claude Code hook guards",
		Long: `Run a named Claude Code hook guard.

Each guard reads hook input from stdin (JSON) and uses the exit-code
protocol to signal its decision to Claude Code:

  0 — allow   (proceed unchanged)
  1 — pass    (no decision; Claude Code keeps its default)
  2 — deny    (block the tool call)
  3 — ask     (pause and ask the user)

Run ` + "`browzer hook <name> --help`" + ` for guard-specific flags.`,
		// Without a sub-name the group prints help.
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	for _, spec := range hookSpecs {
		if spec.factory != nil {
			cmd.AddCommand(spec.factory())
		} else {
			cmd.AddCommand(&cobra.Command{
				Use:   spec.name,
				Short: spec.short,
				RunE:  stubRunE(spec.name),
			})
		}
	}

	return cmd
}
