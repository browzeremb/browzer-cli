package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// errLockTimeoutExitCode is a sentinel returned from RunE to signal that the
// cobra root should translate the error into exit code 16 (lock contention).
// The main.go handler detects CliError.ExitCode == 16.
var errLockTimeoutExitCode = &cliErrors.CliError{
	Message:  "workflow lock timeout",
	ExitCode: 16,
}

// acquireMutatorLock acquires the advisory lock for a workflow mutation command.
// If noLock is true, it emits a warning and returns nil (no lock held, lockHeld=0).
// On ErrLockTimeout it prints a message and returns errLockTimeoutExitCode.
func acquireMutatorLock(cmd *cobra.Command, wfPath string, noLock bool, timeout time.Duration) (*wf.Lock, time.Duration, error) {
	if noLock {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: --no-lock bypass active\n")
		return nil, 0, nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	lockStart := time.Now()
	lock, err := wf.NewLock(wfPath, timeout, cmd.ErrOrStderr())
	if err != nil {
		return nil, 0, err
	}
	if acquireErr := lock.Acquire(); acquireErr != nil {
		if errors.Is(acquireErr, wf.ErrLockTimeout) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"lock timeout: another browzer workflow command is mutating %s\n", wfPath)
			return nil, 0, wf.ErrLockTimeout
		}
		return nil, 0, acquireErr
	}
	return lock, time.Since(lockStart), nil
}

// saveWorkflow serialises the raw map back to pretty-printed JSON and writes
// atomically. It also validates before saving; if validation fails, it returns
// an error and does NOT write.
func saveWorkflow(path string, raw map[string]any) error {
	// Re-encode to typed for validation.
	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal workflow for validation: %w", err)
	}
	var typed wf.Workflow
	if err := json.Unmarshal(b, &typed); err != nil {
		return fmt.Errorf("re-parse workflow for validation: %w", err)
	}
	errs := wf.Validate(typed)
	if len(errs) > 0 {
		return fmt.Errorf("validation error: %s: %s", errs[0].Path, errs[0].Message)
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow: %w", err)
	}
	return wf.AtomicWrite(path, append(out, '\n'))
}

// findStepInRaw finds a step map by its stepId from the raw workflow map.
// Returns the step and its index, or an error if not found.
func findStepInRaw(raw map[string]any, stepID string) (map[string]any, int, error) {
	stepsRaw, ok := raw["steps"]
	if !ok {
		return nil, -1, fmt.Errorf("step %q not found: workflow has no steps", stepID)
	}
	stepsSlice, ok := stepsRaw.([]any)
	if !ok {
		return nil, -1, fmt.Errorf("steps field is not an array")
	}
	for i, s := range stepsSlice {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if sm["stepId"] == stepID {
			return sm, i, nil
		}
	}
	return nil, -1, fmt.Errorf("step %q not found in workflow", stepID)
}

// recomputeCounters recomputes totalSteps and completedSteps in the raw map
// and stamps updatedAt.
func recomputeCounters(raw map[string]any) {
	stepsRaw := raw["steps"]
	stepsSlice, _ := stepsRaw.([]any)
	total := len(stepsSlice)
	completed := 0
	for _, s := range stepsSlice {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if sm["status"] == wf.StatusCompleted {
			completed++
		}
	}
	raw["totalSteps"] = total
	raw["completedSteps"] = completed
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
}

// readPayload reads JSON payload from --payload flag file or stdin (when flag is "").
func readPayload(cmd *cobra.Command, payloadFlag string) ([]byte, error) {
	if payloadFlag != "" {
		return os.ReadFile(payloadFlag)
	}
	return io.ReadAll(cmd.InOrStdin())
}
