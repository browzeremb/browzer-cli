// Package workflow — bootstrap.go
//
// Seeds an empty schema v1 workflow.json. Used by the `browzer workflow init`
// subcommand AND by `set-config` when invoked against a non-existent path
// (the canonical first call from orchestrate-task-delivery's Step 0 mode
// resolution). Without auto-bootstrap, operators have no sanctioned way to
// create the initial file — every other CLI verb requires it to exist.
//
// Idempotency: if the path already exists, BootstrapSkeleton is a no-op and
// returns ErrAlreadyExists. Callers that want create-or-leave-alone semantics
// can ignore the error; callers that want a strict "init" verb can surface it.

package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrAlreadyExists is returned by BootstrapSkeleton when the target path
// already has a file. The caller decides whether to treat it as an error
// (init verb) or a no-op (set-config auto-bootstrap).
var ErrAlreadyExists = errors.New("workflow.json already exists")

// BootstrapOptions tunes the seed contents. All fields are optional —
// sensible defaults derive featureId from the feat-dir name.
type BootstrapOptions struct {
	// FeatureID overrides the auto-derived feature id (default: parent dir
	// name when path is `<...>/docs/browzer/feat-<slug>/workflow.json`).
	FeatureID string
	// FeatureName is the human-readable feature label.
	FeatureName string
	// OriginalRequest is the operator's verbatim ask.
	OriginalRequest string
	// OperatorLocale defaults to "en-US".
	OperatorLocale string
}

// BootstrapSkeleton creates a minimal valid schema v1 workflow.json at path.
// Returns ErrAlreadyExists if the file is present (no overwrite). Creates
// missing parent directories as a courtesy — operators routinely run init
// from inside a fresh feat dir that has no .gitkeep.
func BootstrapSkeleton(path string, opts BootstrapOptions) error {
	if _, err := os.Stat(path); err == nil {
		return ErrAlreadyExists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat workflow path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	featureID := opts.FeatureID
	if featureID == "" {
		// Derive from path: ".../docs/browzer/feat-<slug>/workflow.json"
		// → "feat-<slug>". Defensive against malformed paths.
		dir := filepath.Base(filepath.Dir(path))
		if strings.HasPrefix(dir, "feat-") {
			featureID = dir
		}
	}

	featDir := filepath.Dir(path)
	// Prefer a relative-to-cwd presentation when the absolute path lives
	// under the cwd — keeps the JSON portable across worktrees.
	if cwd, err := os.Getwd(); err == nil {
		if rel, relErr := filepath.Rel(cwd, featDir); relErr == nil &&
			!strings.HasPrefix(rel, "..") {
			featDir = rel
		}
	}

	locale := opts.OperatorLocale
	if locale == "" {
		locale = "en-US"
	}

	skeleton := map[string]any{
		"schemaVersion":   1,
		"featureId":       featureID,
		"featureName":     opts.FeatureName,
		"featDir":         featDir,
		"originalRequest": opts.OriginalRequest,
		"operator":        map[string]any{"locale": locale},
		"config":          map[string]any{},
		"startedAt":       now,
		"updatedAt":       now,
		"totalElapsedMin": 0,
		"currentStepId":   "",
		"nextStepId":      "",
		"totalSteps":      0,
		"completedSteps":  0,
		"notes":           []any{},
		"globalWarnings":  []any{},
		"steps":           []any{},
	}

	data, err := json.MarshalIndent(skeleton, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal skeleton: %w", err)
	}
	data = append(data, '\n')

	return AtomicWrite(path, data)
}
