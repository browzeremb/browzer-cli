package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/spf13/cobra"
)

// resolveWorkflowPathForGetSave returns the absolute path of workflow.json for
// either a top-level command (`browzer get-step PRD --id <feat>`) or a
// workflow-subcommand (`browzer workflow get-step PRD`).
func resolveWorkflowPathForGetSave(cmd *cobra.Command, idFlag string, topLevel bool) (string, error) {
	if topLevel {
		if idFlag == "" {
			return "", fmt.Errorf("--id is required (use the feature slug, e.g. feat-foo)")
		}
		// Reject any slug that escapes the expected subtree.
		if strings.ContainsAny(idFlag, `/\`) || idFlag == ".." || strings.Contains(idFlag, "..") {
			return "", fmt.Errorf("--id must be a plain slug (no path separators or traversal): %q", idFlag)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		baseAbs, err := filepath.Abs(filepath.Join(cwd, "docs", "browzer"))
		if err != nil {
			return "", err
		}
		abs, err := filepath.Abs(filepath.Join(baseAbs, idFlag, "workflow.json"))
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(baseAbs, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("resolved path %q escapes %q", abs, baseAbs)
		}
		return abs, nil
	}
	return getWorkflowPath(cmd)
}

// cliExitErr wraps an error with a specific exit code so main.go's error
// translator routes it correctly.
func cliExitErr(code int, err error) error {
	if err == nil {
		return nil
	}
	return &cliErrors.CliError{
		Message:  err.Error(),
		ExitCode: code,
	}
}
