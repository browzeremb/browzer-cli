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
	"syscall"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/commands"
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

	root := commands.NewRootCommand(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %s\n", err.Error())
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
