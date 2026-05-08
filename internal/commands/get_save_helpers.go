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
//
// Resolution order:
//  1. --id wins when provided, regardless of which command surface is calling.
//  2. When idFlag is empty and topLevel=true, returns an error (--id required).
//  3. When idFlag is empty and topLevel=false, falls through to getWorkflowPath
//     which reads --workflow / env / walk-up discovery.
func resolveWorkflowPathForGetSave(cmd *cobra.Command, idFlag string, topLevel bool) (string, error) {
	// --id wins when provided, regardless of which command surface is calling.
	if idFlag != "" {
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
	if topLevel {
		return "", fmt.Errorf("--id is required (use the feature slug, e.g. feat-foo)")
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
