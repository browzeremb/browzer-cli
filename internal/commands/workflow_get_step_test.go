package commands

// workflow_get_step_test.go — tests for R-4 (--exit-only flag) on
// `browzer get-step`. The flag suppresses stdout/stderr on error so
// callers can probe step existence using only the exit code.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
)

// minimalWorkflowNoCodeReview is a valid v2 workflow with no steps at all.
// Used to verify --exit-only behaviour when the requested phase is absent.
const minimalWorkflowNoCodeReview = `{
  "schemaVersion": 2,
  "featureId": "feat-20260512-exit-only-test",
  "featureName": "exit-only-test",
  "featDir": "docs/browzer/feat-20260512-exit-only-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "executionStrategy": "serial", "setAt": "2026-05-12T00:00:00Z"},
  "startedAt": "2026-05-12T00:00:00Z",
  "updatedAt": "2026-05-12T00:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}
`

// extractExitCodeFromErr extracts the CliError exit code from an error,
// returning 1 as the default when the error chain contains no CliError.
// Local to this file to avoid a redeclaration conflict with savestep_test.go.
func extractExitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	var ce *cliErrors.CliError
	if errors.As(err, &ce) {
		return ce.ExitCode
	}
	return 1
}

// TestGetStep_ExitOnly verifies R-4: when --exit-only is set and the requested
// phase does not exist, the command exits 2 with completely empty stdout AND
// stderr. A counterfactual sub-test verifies that omitting --exit-only still
// exits 2 but writes the expected "no step found" message to stderr.
func TestGetStep_ExitOnly(t *testing.T) {
	feat := "feat-20260512-exit-only-test"

	t.Run("exits_2_with_empty_stdout_stderr_when_step_absent", func(t *testing.T) {
		root, _ := seedWorkflowForGetStep(t, feat, minimalWorkflowNoCodeReview)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		t.Chdir(root)

		var stdout, stderr bytes.Buffer
		cmd := NewRootCommand("test")
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"get-step", "CODE_REVIEW", "--id", feat, "--exit-only"})

		err := cmd.Execute()

		// Assert: exit code 2.
		code := extractExitCodeFromErr(err)
		if code != 2 {
			t.Errorf("expected exit code 2 with --exit-only; got %d (err=%v)", code, err)
		}

		// Assert: stdout is empty.
		if stdout.Len() != 0 {
			t.Errorf("expected empty stdout with --exit-only on missing step; got %q", stdout.String())
		}

		// Assert: stderr is empty.
		if stderr.Len() != 0 {
			t.Errorf("expected empty stderr with --exit-only on missing step; got %q", stderr.String())
		}
	})

	t.Run("exits_2_with_empty_stdout_stderr_for_unknown_phase", func(t *testing.T) {
		root, _ := seedWorkflowForGetStep(t, feat, minimalWorkflowNoCodeReview)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		t.Chdir(root)

		var stdout, stderr bytes.Buffer
		cmd := NewRootCommand("test")
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"get-step", "NONEXISTENT_PHASE", "--id", feat, "--exit-only"})

		err := cmd.Execute()

		code := extractExitCodeFromErr(err)
		if code != 2 {
			t.Errorf("expected exit code 2 with --exit-only on unknown phase; got %d (err=%v)", code, err)
		}
		if stdout.Len() != 0 {
			t.Errorf("expected empty stdout with --exit-only on unknown phase; got %q", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("expected empty stderr with --exit-only on unknown phase; got %q", stderr.String())
		}
	})

	t.Run("counterfactual_no_exit_only_preserves_descriptive_error", func(t *testing.T) {
		root, _ := seedWorkflowForGetStep(t, feat, minimalWorkflowNoCodeReview)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		t.Chdir(root)

		var stdout, stderr bytes.Buffer
		cmd := NewRootCommand("test")
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		// No --exit-only: existing behavior must be preserved.
		cmd.SetArgs([]string{"get-step", "CODE_REVIEW", "--id", feat})

		err := cmd.Execute()

		// Exit code must still be 2 (or non-zero error).
		if err == nil {
			t.Fatal("expected error for absent CODE_REVIEW step without --exit-only; got nil")
		}
		code := extractExitCodeFromErr(err)
		if code != 2 {
			t.Errorf("expected exit code 2 without --exit-only; got %d", code)
		}

		// The returned error message must contain the phase name (descriptive error),
		// confirming existing behavior is preserved. Without --exit-only, the CliError
		// carries the full message (used by main.go to print to os.Stderr).
		// Note: cobra's SilenceErrors=true on root prevents it from writing to
		// cmd.SetErr; only RunE's explicit cmd.ErrOrStderr() calls appear there.
		errMsg := err.Error()
		if !strings.Contains(errMsg, "CODE_REVIEW") {
			t.Errorf("expected error message to contain phase name 'CODE_REVIEW' without --exit-only; got %q", errMsg)
		}
		// stdout must be empty (step not found → no output).
		if stdout.Len() != 0 {
			t.Errorf("expected empty stdout on missing step; got %q", stdout.String())
		}
	})

	t.Run("success_path_unaffected_by_exit_only", func(t *testing.T) {
		// CONFIG is a virtual phase that always succeeds — verify that --exit-only
		// does not interfere with the success path (stdout still populated).
		root, _ := seedWorkflowForGetStep(t, feat, minimalWorkflowNoCodeReview)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		t.Chdir(root)

		var stdout, stderr bytes.Buffer
		cmd := NewRootCommand("test")
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"get-step", "CONFIG", "--id", feat, "--exit-only", "--json"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("expected success for CONFIG with --exit-only; got error: %v\nstderr=%s", err, stderr.String())
		}
		if stdout.Len() == 0 {
			t.Error("expected non-empty stdout on success path with --exit-only")
		}
		if stderr.Len() != 0 {
			t.Errorf("expected empty stderr on success path with --exit-only; got %q", stderr.String())
		}
	})

	// F-013: pin the pre-existing behavior where omitting --id (cobra's
	// MarkFlagRequired fires BEFORE RunE) surfaces cobra's own required-flag
	// error even when --exit-only is present. The exit-only guard in RunE is
	// never reached because cobra rejects the invocation during flag parsing.
	t.Run("omitting_id_still_surfaces_cobra_required_flag_error", func(t *testing.T) {
		root, _ := seedWorkflowForGetStep(t, feat, minimalWorkflowNoCodeReview)
		t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
		t.Chdir(root)

		var stdout, stderr bytes.Buffer
		cmd := NewRootCommand("test")
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		// --id is intentionally omitted; --exit-only is set. Cobra fires its
		// required-flag validation before RunE, so the silent path is never taken.
		cmd.SetArgs([]string{"get-step", "CODE_REVIEW", "--exit-only"})

		err := cmd.Execute()

		// Cobra surfaces a non-nil error for the missing required flag.
		if err == nil {
			t.Fatal("expected error when --id is omitted; got nil")
		}

		// The error or stderr must reference "id" (cobra's required-flag message).
		errMsg := err.Error()
		stderrMsg := stderr.String()
		mentionsID := strings.Contains(errMsg, "id") || strings.Contains(stderrMsg, "id")
		if !mentionsID {
			t.Errorf("expected error/stderr to mention 'id' for missing required flag; err=%q stderr=%q", errMsg, stderrMsg)
		}

		// stdout must be empty — no step output was produced.
		if stdout.Len() != 0 {
			t.Errorf("expected empty stdout when --id is omitted; got %q", stdout.String())
		}
	})
}
