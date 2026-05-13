package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/browzeremb/browzer-cli/internal/commands/runfilters"
	"github.com/spf13/cobra"
)

// maxStdoutBytes caps in-memory buffering of child stdout (64 MiB).
// When exceeded, capture is truncated and a notice is appended.
const maxStdoutBytes = 64 * 1024 * 1024

func registerRun(parent *cobra.Command) { parent.AddCommand(newRunCommand()) }

func newRunCommand() *cobra.Command {
	var noFilter bool

	cmd := &cobra.Command{
		Use:   "run <cmd> [args...]",
		Short: "Run a command with token-optimized output compression",
		Long: `Runs the given command and compresses its stdout output to reduce LLM token consumption.

Compression is applied per command category (git, vitest, turbo, go test, cargo test, biome, tsc).
When the command exits with a non-zero code, the raw output is always returned — never compressed.

Analogous to RTK (Rust Token Killer) but integrated with the browzer token economy ledger.

Note: browzer run inherits the caller's $PATH for child-binary resolution.

Examples:
  browzer run git status
  browzer run git diff
  browzer run pnpm turbo test
  browzer run vitest
  browzer run go test ./...
  browzer run cargo test
  browzer run biome check .
  browzer run tsc --noEmit
  browzer run --no-filter git log   # bypass compression
  browzer run -- git commit -m "feat: foo"  # use -- to pass flags verbatim`,
		Args: cobra.MinimumNArgs(1),
		// DisableFlagParsing passes all argv after "browzer run" verbatim to the
		// child process, including flags like -m "msg" that cobra would otherwise
		// consume or strip. We manually parse --no-filter before forwarding.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// With DisableFlagParsing=true all args (including --no-filter) arrive
			// unprocessed. Intercept --help / -h first so the smoke test and
			// interactive users get the Cobra help text instead of an exec error.
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}

			// Strip --no-filter / -- separator and collect the rest as child argv.
			childArgs := make([]string, 0, len(args))
			seenSep := false
			for _, a := range args {
				switch {
				case !seenSep && a == "--no-filter":
					noFilter = true
				case !seenSep && a == "--":
					seenSep = true // everything after -- goes to child verbatim
				default:
					childArgs = append(childArgs, a)
				}
			}

			if len(childArgs) == 0 {
				return fmt.Errorf("run: no child command specified")
			}

			// Resolve filter for this command pattern.
			filter := runfilters.DefaultRegistry.Resolve(childArgs)
			if noFilter {
				filter = nil
			}

			// Execute the child command.
			child := exec.Command(childArgs[0], childArgs[1:]...) //nolint:gosec // intentional subprocess proxy
			child.Stdin = os.Stdin
			child.Stderr = os.Stderr // always pass stderr through raw

			var stdoutBuf bytes.Buffer
			child.Stdout = &stdoutBuf

			childStart := time.Now()
			err := child.Run()
			execMs := int(time.Since(childStart).Milliseconds())
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
					// ExitCode() returns -1 when the child was killed by a signal.
					// Map to 128+signum to honour the shell convention (SIGINT=130,
					// SIGTERM=143) that browzer root.go's rootExitCodes documents.
					if exitCode == -1 {
						if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
							exitCode = 128 + int(ws.Signal())
						} else {
							exitCode = 1
						}
					}
				} else {
					return fmt.Errorf("run: failed to execute %q: %w", childArgs[0], err)
				}
			}

			raw := capStdout(&stdoutBuf)

			filterLevel := runfilters.DefaultRegistry.CategoryOf(childArgs)

			// Exit gate: non-zero exit or no filter — return raw output.
			if exitCode != 0 || filter == nil {
				_, _ = os.Stdout.Write(raw)
				// Track passthrough event (best-effort).
				// Use distinct source tags so telemetry can distinguish opt-out
				// (--no-filter) from unknown-command (no registered filter).
				source := "browzer-run-unknown"
				if noFilter {
					source = "browzer-run-no-filter"
				}
				if exitCode != 0 {
					source = "browzer-run-exitfail"
				}
				_ = runfilters.TrackRun(source, raw, raw, filterLevel, execMs)
				os.Exit(exitCode)
			}

			// Apply filter and emit compressed output.
			compressed := filter(raw)
			_, _ = os.Stdout.Write(compressed)

			// Track savings event (best-effort).
			_ = runfilters.TrackRun(
				"browzer-run-"+filterLevel,
				raw,
				compressed,
				filterLevel,
				execMs,
			)

			return nil
		},
	}

	return cmd
}

// capStdout returns the buffer contents capped at maxStdoutBytes.
// When the cap is exceeded it appends a truncation notice so the consumer
// (filter or raw passthrough) knows output was elided.
func capStdout(buf *bytes.Buffer) []byte {
	raw := buf.Bytes()
	if len(raw) <= maxStdoutBytes {
		// Return a copy so the buffer can be GC'd.
		out := make([]byte, len(raw))
		copy(out, raw)
		return out
	}
	prefix := make([]byte, maxStdoutBytes)
	copy(prefix, raw[:maxStdoutBytes])
	notice := fmt.Sprintf("\n... (%d MiB elided — output exceeded browzer run cap)\n",
		(len(raw)-maxStdoutBytes)/(1024*1024)+1)
	return append(prefix, []byte(notice)...)
}
