package workflow

import "fmt"

// ValidationError records a single structural violation in a Workflow document.
type ValidationError struct {
	// Path is a dot-separated JSON path to the offending field (e.g. "schemaVersion").
	Path string
	// Message is a human-readable description of the violation.
	Message string
}

func (e ValidationError) String() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// legalStatuses is the set of accepted StepStatus values.
var legalStatuses = map[StepStatus]bool{
	StatusPending:         true,
	StatusRunning:         true,
	StatusAwaitingReview:  true,
	StatusCompleted:       true,
	StatusPausedPendingOp: true,
	StatusSkipped:         true,
	StatusStopped:         true,
}

// legalNames is the set of accepted StepName values.
var legalNames = map[StepName]bool{
	StepBrainstorming:       true,
	StepPRD:                 true,
	StepTasksManifest:       true,
	StepTask:                true,
	StepCodeReview:          true,
	StepReceivingCodeReview: true,
	StepWriteTests:          true,
	StepUpdateDocs:          true,
	StepFeatureAcceptance:   true,
	StepCommit:              true,
	// Deprecated FIX_FINDINGS still accepted so historical workflow.json files validate.
	StepFixFindings: true,
}

// legalConfigModes is the set of accepted config.mode values.
var legalConfigModes = map[string]bool{
	"autonomous": true,
	"review":     true,
}

// Validate performs structural validation on a Workflow document.
// It returns a slice of ValidationError values describing each violation found.
// An empty slice indicates a structurally valid document.
func Validate(wf Workflow) []ValidationError {
	var errs []ValidationError

	// Schema cutover (WF-SYNC-1, 2026-05-04): schemaVersion=2 is the new
	// canonical version. v1 documents stay readable for backwards-compat
	// (the judge rubric ignores violations newer than the workflow's
	// startedAt) but new mutations must produce v2-shaped output.
	// Tolerate both values during the migration window.
	if wf.SchemaVersion != 1 && wf.SchemaVersion != 2 {
		errs = append(errs, ValidationError{
			Path:    "schemaVersion",
			Message: fmt.Sprintf("must be 1 or 2, got %d", wf.SchemaVersion),
		})
	}

	if wf.FeatureID == "" {
		errs = append(errs, ValidationError{
			Path:    "featureId",
			Message: "must not be empty",
		})
	}

	if wf.Config.Mode != "" && !legalConfigModes[wf.Config.Mode] {
		errs = append(errs, ValidationError{
			Path:    "config.mode",
			Message: fmt.Sprintf("unrecognised value %q; must be 'autonomous' or 'review'", wf.Config.Mode),
		})
	}

	for i, step := range wf.Steps {
		prefix := fmt.Sprintf("steps[%d]", i)

		if step.StepID == "" {
			errs = append(errs, ValidationError{
				Path:    prefix + ".stepId",
				Message: "must not be empty",
			})
		}

		if step.Name != "" && !legalNames[step.Name] {
			errs = append(errs, ValidationError{
				Path:    prefix + ".name",
				Message: fmt.Sprintf("unrecognised step name %q", step.Name),
			})
		}

		if step.Status != "" && !legalStatuses[step.Status] {
			errs = append(errs, ValidationError{
				Path:    prefix + ".status",
				Message: fmt.Sprintf("unrecognised status %q", step.Status),
			})
		}

		// TASK steps require a taskId.
		if step.Name == StepTask && step.TaskID == "" {
			errs = append(errs, ValidationError{
				Path:    prefix + ".taskId",
				Message: "TASK step must have a non-empty taskId",
			})
		}
	}

	return errs
}
