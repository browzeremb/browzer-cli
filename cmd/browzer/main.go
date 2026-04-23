// Browzer CLI entrypoint.
//
// Wires cobra to all v1 commands. The signal handlers translate SIGINT
// and SIGTERM into the conventional 130/143 exit codes (matching the
// Node CLI's behavior). Top-level recover() converts panics to a clean
// stderr message + exit 1 instead of a stack dump.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/browzeremb/browzer-cli/internal/commands"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// version is injected at build time via:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/browzer
//
// goreleaser sets this automatically. Falls back to "dev" for local builds.
var version = "dev"

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "\nUncaught panic: %v\n", r)
			os.Exit(1)
		}
	}()

	installSignalHandlers()

	// Forward the ldflags-injected version to the daemon start path so
	// telemetry batches include `cliVersion` in their payload.
	commands.SetDaemonVersion(version)

	root := commands.NewRootCommand(version)
	if err := root.Execute(); err != nil {
		msg := err.Error()
		fmt.Fprintf(os.Stderr, "\nError: %s\n", msg)
		// Unknown command with no close match — point the agent at the
		// command list. Cobra already appends "Did you mean X?" when a
		// suggestion exists, so only add the hint when it didn't.
		if strings.HasPrefix(msg, "unknown command") && !strings.Contains(msg, "Did you mean") {
			fmt.Fprintf(os.Stderr, "Common commands: init, login, workspace, explore, search, deps, ask. Run `browzer --help` for the full list.\n")
		}
		var cliErr *cliErrors.CliError
		if errors.As(err, &cliErr) {
			os.Exit(cliErr.ExitCode)
		}
		os.Exit(1)
	}
}

// installSignalHandlers wires SIGINT/SIGTERM to the conventional exit
// codes 130/143. Mirrors the Node CLI's signal handling, see
// packages/cli/src/index.ts (legacy).
func installSignalHandlers() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "\nReceived %s — aborting.\n", sig.String())
		switch sig {
		case syscall.SIGINT:
			os.Exit(130)
		case syscall.SIGTERM:
			os.Exit(143)
		default:
			os.Exit(1)
		}
	}()
}
