package workflow

import (
	"strings"
	"testing"
)

// TestValidate_AcceptsTopLevelSkeleton verifies that Validate accepts a
// minimal but structurally correct schema-v1 document.
// Covers T1-T-6: Validate accepts top-level skeleton.
func TestValidate_AcceptsTopLevelSkeleton(t *testing.T) {
	wf := Workflow{
		SchemaVersion: 1,
		FeatureID:     "feat-test",
		FeatureName:   "Test",
		FeatDir:       "docs/browzer/feat-test",
		Config:        WorkflowConfig{Mode: "autonomous"},
		Steps:         []Step{},
	}
	errs := Validate(wf)
	if len(errs) != 0 {
		t.Errorf("expected no validation errors for skeleton, got: %v", errs)
	}
}

// TestValidate_AcceptsEachStepTypeName verifies that Validate accepts steps
// with each legal step type name.
// Covers: each step type (BRAINSTORMING, PRD, TASKS_MANIFEST, TASK,
// CODE_REVIEW, RECEIVING_CODE_REVIEW, WRITE_TESTS, UPDATE_DOCS,
// FEATURE_ACCEPTANCE, COMMIT, FIX_FINDINGS — last is deprecated but still legal).
func TestValidate_AcceptsEachStepTypeName(t *testing.T) {
	stepNames := []string{
		"BRAINSTORMING", "PRD", "TASKS_MANIFEST", "TASK",
		"CODE_REVIEW", "RECEIVING_CODE_REVIEW", "WRITE_TESTS",
		"UPDATE_DOCS", "FEATURE_ACCEPTANCE", "COMMIT", "FIX_FINDINGS",
	}
	for _, name := range stepNames {
		t.Run(name, func(t *testing.T) {
			step := Step{
				StepID: "STEP_01",
				Name:   StepName(name),
				Status: "PENDING",
			}
			if name == "TASK" {
				step.TaskID = "TASK_01"
			}
			wf := Workflow{
				SchemaVersion: 1,
				FeatureID:     "feat-test",
				FeatureName:   "Test",
				FeatDir:       "docs/browzer/feat-test",
				Config:        WorkflowConfig{Mode: "autonomous"},
				Steps:         []Step{step},
			}
			errs := Validate(wf)
			if len(errs) != 0 {
				t.Errorf("step type %q: unexpected errors: %v", name, errs)
			}
		})
	}
}

// TestValidate_AcceptsAllLifecycleStatuses verifies that all legal status
// values pass validation.
// Covers T1-T-6: each lifecycle status.
func TestValidate_AcceptsAllLifecycleStatuses(t *testing.T) {
	statuses := []string{
		"PENDING", "RUNNING", "AWAITING_REVIEW", "COMPLETED",
		"PAUSED_PENDING_OPERATOR", "SKIPPED", "STOPPED",
	}
	for _, s := range statuses {
		t.Run(s, func(t *testing.T) {
			wf := Workflow{
				SchemaVersion: 1,
				FeatureID:     "feat-test",
				FeatureName:   "Test",
				FeatDir:       "docs/browzer/feat-test",
				Config:        WorkflowConfig{Mode: "autonomous"},
				Steps: []Step{
					{StepID: "STEP_01", Name: "BRAINSTORMING", Status: StepStatus(s)},
				},
			}
			errs := Validate(wf)
			if len(errs) != 0 {
				t.Errorf("status %q: unexpected errors: %v", s, errs)
			}
		})
	}
}

// TestValidate_RejectsMissingSchemaVersion verifies that a missing/zero
// schemaVersion is reported as a validation error.
// Covers T1-T-6: rejects missing required fields.
func TestValidate_RejectsMissingSchemaVersion(t *testing.T) {
	wf := Workflow{
		SchemaVersion: 0, // missing / zero
		FeatureID:     "feat-test",
		FeatureName:   "Test",
		FeatDir:       "docs/browzer/feat-test",
		Config:        WorkflowConfig{Mode: "autonomous"},
		Steps:         []Step{},
	}
	errs := Validate(wf)
	if len(errs) == 0 {
		t.Error("expected error for missing schemaVersion, got none")
	}
	if !containsPath(errs, "schemaVersion") {
		t.Errorf("expected error referencing 'schemaVersion', got: %v", errs)
	}
}

// TestValidate_RejectsMissingFeatureID verifies that an empty featureId is
// reported as a validation error.
// Covers T1-T-6: rejects missing required fields.
func TestValidate_RejectsMissingFeatureID(t *testing.T) {
	wf := Workflow{
		SchemaVersion: 1,
		FeatureID:     "", // missing
		FeatureName:   "Test",
		FeatDir:       "docs/browzer/feat-test",
		Config:        WorkflowConfig{Mode: "autonomous"},
		Steps:         []Step{},
	}
	errs := Validate(wf)
	if len(errs) == 0 {
		t.Error("expected error for missing featureId, got none")
	}
	if !containsPath(errs, "featureId") {
		t.Errorf("expected error referencing 'featureId', got: %v", errs)
	}
}

// TestValidate_RejectsIllegalStatusValue verifies that an unrecognised step
// status value is rejected with a path+message.
// Covers T1-T-6: rejects illegal status values.
func TestValidate_RejectsIllegalStatusValue(t *testing.T) {
	wf := Workflow{
		SchemaVersion: 1,
		FeatureID:     "feat-test",
		FeatureName:   "Test",
		FeatDir:       "docs/browzer/feat-test",
		Config:        WorkflowConfig{Mode: "autonomous"},
		Steps: []Step{
			{StepID: "STEP_01", Name: "BRAINSTORMING", Status: "NOT_A_REAL_STATUS"},
		},
	}
	errs := Validate(wf)
	if len(errs) == 0 {
		t.Error("expected error for illegal status value, got none")
	}
	if !containsPath(errs, "status") {
		t.Errorf("expected error referencing 'status', got: %v", errs)
	}
}

// TestValidate_RejectsIllegalConfigMode verifies that an unrecognised config.mode
// value is rejected.
// Covers T1-T-6: rejects illegal status values (config.mode variant).
func TestValidate_RejectsIllegalConfigMode(t *testing.T) {
	wf := Workflow{
		SchemaVersion: 1,
		FeatureID:     "feat-test",
		FeatureName:   "Test",
		FeatDir:       "docs/browzer/feat-test",
		Config:        WorkflowConfig{Mode: "INVALID_MODE"},
		Steps:         []Step{},
	}
	errs := Validate(wf)
	if len(errs) == 0 {
		t.Error("expected error for illegal config.mode, got none")
	}
	if !containsPath(errs, "config.mode") {
		t.Errorf("expected error referencing 'config.mode', got: %v", errs)
	}
}

// TestValidate_RejectsMalformedTaskStep verifies that a TASK step missing its
// taskId is rejected.
// Covers T1-T-6: rejects malformed step payloads.
func TestValidate_RejectsMalformedTaskStep(t *testing.T) {
	wf := Workflow{
		SchemaVersion: 1,
		FeatureID:     "feat-test",
		FeatureName:   "Test",
		FeatDir:       "docs/browzer/feat-test",
		Config:        WorkflowConfig{Mode: "autonomous"},
		Steps: []Step{
			{
				StepID: "STEP_04_TASK_01",
				Name:   "TASK",
				Status: "PENDING",
				TaskID: "", // missing — malformed TASK step
			},
		},
	}
	errs := Validate(wf)
	if len(errs) == 0 {
		t.Error("expected error for TASK step missing taskId, got none")
	}
	if !containsPath(errs, "taskId") {
		t.Errorf("expected error referencing 'taskId', got: %v", errs)
	}
}

// containsPath returns true if any ValidationError's path contains the given
// substring.
func containsPath(errs []ValidationError, pathSubstr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Path, pathSubstr) {
			return true
		}
	}
	return false
}
