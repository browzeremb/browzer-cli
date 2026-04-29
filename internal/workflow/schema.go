package workflow

import "encoding/json"

// StepName identifies the type of a workflow step.
type StepName = string

// Legal StepName values.
const (
	StepBrainstorming       StepName = "BRAINSTORMING"
	StepPRD                 StepName = "PRD"
	StepTasksManifest       StepName = "TASKS_MANIFEST"
	StepTask                StepName = "TASK"
	StepCodeReview          StepName = "CODE_REVIEW"
	StepReceivingCodeReview StepName = "RECEIVING_CODE_REVIEW"
	StepWriteTests          StepName = "WRITE_TESTS"
	StepUpdateDocs          StepName = "UPDATE_DOCS"
	StepFeatureAcceptance   StepName = "FEATURE_ACCEPTANCE"
	StepCommit              StepName = "COMMIT"
	// Deprecated: replaced by StepReceivingCodeReview in the receiving-code-review redesign.
	// Kept in legalNames so historical workflow.json files still validate.
	StepFixFindings StepName = "FIX_FINDINGS"
)

// StepStatus is the lifecycle status of a step.
type StepStatus = string

// Legal StepStatus values.
const (
	StatusPending              StepStatus = "PENDING"
	StatusRunning              StepStatus = "RUNNING"
	StatusAwaitingReview       StepStatus = "AWAITING_REVIEW"
	StatusCompleted            StepStatus = "COMPLETED"
	StatusPausedPendingOp      StepStatus = "PAUSED_PENDING_OPERATOR"
	StatusSkipped              StepStatus = "SKIPPED"
	StatusStopped              StepStatus = "STOPPED"
)

// OperatorInfo carries locale and optional operator-specific metadata.
type OperatorInfo struct {
	Locale string         `json:"locale,omitempty"`
	Extra  map[string]any `json:"-"`
}

// WorkflowConfig is the operator-chosen configuration section.
type WorkflowConfig struct {
	// Mode is "autonomous" or "review".
	Mode  string `json:"mode,omitempty"`
	SetAt string `json:"setAt,omitempty"`
}

// StepApplicability records whether a step was applicable to the feature.
type StepApplicability struct {
	Applicable bool   `json:"applicable"`
	Reason     string `json:"reason,omitempty"`
}

// WorktreeEntry describes a single git worktree associated with a step.
type WorktreeEntry struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
}

// StepWorktrees records worktree usage for a step.
type StepWorktrees struct {
	Used      bool            `json:"used"`
	Worktrees []WorktreeEntry `json:"worktrees,omitempty"`
}

// Step represents one step in a workflow. Supported step names:
// BRAINSTORMING, PRD, TASKS_MANIFEST, TASK, CODE_REVIEW, UPDATE_DOCS,
// FEATURE_ACCEPTANCE, COMMIT, FIX_FINDINGS. The task payload is kept as raw
// JSON so that each step type's unique shape survives round-trips without
// schema drift.
type Step struct {
	StepID       string            `json:"stepId,omitempty"`
	Name         StepName          `json:"name,omitempty"`
	TaskID       string            `json:"taskId,omitempty"`
	Status       StepStatus        `json:"status,omitempty"`
	Applicability StepApplicability `json:"applicability,omitempty"`
	StartedAt    string            `json:"startedAt,omitempty"`
	CompletedAt  *string           `json:"completedAt"`
	ElapsedMin   float64           `json:"elapsedMin,omitempty"`
	RetryCount   int               `json:"retryCount,omitempty"`
	ItDependsOn  []string          `json:"itDependsOn,omitempty"`
	NextStep     string            `json:"nextStep,omitempty"`
	SkillsToInvoke []string        `json:"skillsToInvoke,omitempty"`
	SkillsInvoked  []string        `json:"skillsInvoked,omitempty"`
	Owner        *string           `json:"owner"`
	Worktrees    StepWorktrees     `json:"worktrees,omitempty"`
	Warnings     []any             `json:"warnings,omitempty"`
	ReviewHistory []any            `json:"reviewHistory,omitempty"`
	// Task is the step-type-specific payload. Its shape varies by Name.
	Task json.RawMessage `json:"task,omitempty"`
}

// Workflow is the top-level structure of a workflow.json file (schema v1).
type Workflow struct {
	SchemaVersion  int            `json:"schemaVersion"`
	FeatureID      string         `json:"featureId,omitempty"`
	FeatureName    string         `json:"featureName,omitempty"`
	FeatDir        string         `json:"featDir,omitempty"`
	OriginalRequest string        `json:"originalRequest,omitempty"`
	Operator       OperatorInfo   `json:"operator,omitempty"`
	Config         WorkflowConfig `json:"config,omitempty"`
	StartedAt      string         `json:"startedAt,omitempty"`
	UpdatedAt      string         `json:"updatedAt,omitempty"`
	TotalElapsedMin float64       `json:"totalElapsedMin,omitempty"`
	CurrentStepID  string         `json:"currentStepId,omitempty"`
	NextStepID     string         `json:"nextStepId,omitempty"`
	TotalSteps     int            `json:"totalSteps,omitempty"`
	CompletedSteps int            `json:"completedSteps,omitempty"`
	Notes          []any          `json:"notes,omitempty"`
	GlobalWarnings []any          `json:"globalWarnings,omitempty"`
	Steps          []Step         `json:"steps,omitempty"`
}
