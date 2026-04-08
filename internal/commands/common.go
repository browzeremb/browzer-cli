package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/browzeremb/browzer-cli/internal/api"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// rootContext returns a context that is cancelled when the parent
// command is interrupted. For now we use context.Background since
// signal handling lives in cmd/browzer/main.go and exits the process
// directly. Kept as a function so future graceful-shutdown work can
// thread an AbortController-equivalent through every command.
func rootContext(_ *cobra.Command) context.Context {
	return context.Background()
}

// requireGitRoot returns the absolute git root or a CliError exiting 1.
// Mirrors the legacy `findGitRoot` + "Not inside a git repository" error.
func requireGitRoot() (string, error) {
	cwd, _ := os.Getwd()
	root := git.FindGitRoot(cwd)
	if root == "" {
		return "", cliErrors.New("Not inside a git repository.")
	}
	return root, nil
}

// requireAuth wraps NewAuthenticatedClient. timeoutSeconds=0 uses the
// default 30s; pass 600 for sync/init cold-start tolerance.
func requireAuth(timeoutSeconds int) (*api.AuthenticatedClient, error) {
	timeout := api.DefaultTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	return api.NewAuthenticatedClient(timeout)
}

// isTTY returns true when stdin is attached to a terminal. Used to
// gate interactive prompts.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// validateLimit enforces the [1,200] bound on a --limit flag value.
func validateLimit(n int) error {
	if n < 1 || n > 200 {
		return cliErrors.Newf("--limit must be between 1 and 200 (got %d)", n)
	}
	return nil
}

// emitOrFail wraps output.Emit with error annotation.
func emitOrFail(payload any, opts output.Options, human string) error {
	if err := output.Emit(payload, opts, human); err != nil {
		return fmt.Errorf("emit output: %w", err)
	}
	return nil
}

