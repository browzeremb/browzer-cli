// Package workflow provides utilities for locating, locking, reading, writing,
// validating, and querying workflow.json files that track feature delivery state.
package workflow

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ResolveWorkflowPath resolves the workflow.json path using the following
// precedence:
//
//  1. flagPath — if non-empty, returned as-is (no stderr output).
//  2. BROWZER_WORKFLOW env var — if non-empty, returned and logged to stderr.
//  3. Git-style walk-up — searches ancestor directories of cwd for
//     workflow.json files located under a docs/browzer/feat-* directory.
//     The resolved path is logged to stderr.
//
// Returns a descriptive error if none of the three strategies succeeds.
func ResolveWorkflowPath(flagPath, cwd string, stderr io.Writer) (string, error) {
	// Strategy 1: explicit flag wins silently.
	if flagPath != "" {
		return flagPath, nil
	}

	// Strategy 2: env var.
	if envVal := os.Getenv("BROWZER_WORKFLOW"); envVal != "" {
		_, _ = fmt.Fprintf(stderr, "resolved workflow: %s\n", envVal)
		return envVal, nil
	}

	// Strategy 3: walk-up looking for a workflow.json inside a feat dir.
	found, err := walkUpForWorkflow(cwd)
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(stderr, "resolved workflow: %s\n", found)
	return found, nil
}

// walkUpForWorkflow searches cwd and its ancestors for a workflow.json that
// lives inside a docs/browzer/feat-* directory. It first checks whether cwd
// itself is such a directory, then walks up the tree.
func walkUpForWorkflow(cwd string) (string, error) {
	dir := cwd
	for {
		candidate := filepath.Join(dir, "workflow.json")
		if isFeatDir(dir) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}

		// Also search docs/browzer/feat-* subdirs under dir.
		found, err := searchFeatDirs(dir)
		if err == nil && found != "" {
			return found, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("workflow.json not found: not inside a docs/browzer/feat-* directory and BROWZER_WORKFLOW is not set; use --workflow <path>")
}

// isFeatDir reports whether path looks like a docs/browzer/feat-* directory.
func isFeatDir(path string) bool {
	// Normalize separators and check that the last two components are
	// "browzer/<feat-*>" under a "docs" parent.
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "feat") && base != "." {
		return false
	}
	parent := filepath.Base(filepath.Dir(path))
	if parent != "browzer" {
		return false
	}
	grandParent := filepath.Base(filepath.Dir(filepath.Dir(path)))
	return grandParent == "docs"
}

// searchFeatDirs looks for docs/browzer/feat-*/workflow.json under root.
func searchFeatDirs(root string) (string, error) {
	browzerDir := filepath.Join(root, "docs", "browzer")
	entries, err := os.ReadDir(browzerDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "feat") {
			continue
		}
		candidate := filepath.Join(browzerDir, e.Name(), "workflow.json")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}
	return "", errors.New("no feat dir found")
}
