// Single source of truth for docs/browzer/<feat>/workflow.json (schema v2).
// Edit this file ONLY — workflow-v1.schema.json, workflow_v1.go, and
// packages/skills/references/workflow-schema.md are regenerated from it via
// `make ci-check`. See README.md.
//
// Hard cutoff: schemaVersion 2 introduced 2026-05-04 — workflows under v1 are
// read-only post-merge (per WORKFLOW_SYNC_REDESIGN.md §5).
//
// @addedIn dating convention:
//   2026-04-24T00:00:00Z — pre-existing fields from schema v1 (workflow.json
//                          introduction).
//   2026-04-30T18:00:00Z — fields added in feat-20260430-ci-flake-strike retro
//                          (kind enum, executionDepth, commandSource).
//   2026-05-04T00:00:00Z — fields newly required by this PR (totalElapsedMin,
//                          completedAt, elapsedMin, dispatches[], pluginVersion).
//   2026-05-06T00:00:00Z — RETRO PR 6: #TestSpec.type → intent (semantic
//                          rename — CONTRACT BREAK, no alias) + scope enum;
//                          #FileChange kind expanded with "rename-domain".
package workflow

import "time"

// =============================================================
// Top-level workflow
// =============================================================
#WorkflowV1: {
	schemaVersion:    2                                       @addedIn("2026-05-04T00:00:00Z")
	pluginVersion:    *null | string                          @addedIn("2026-05-04T00:00:00Z")
	featureId:        =~"^feat-[0-9]{8}-[a-z0-9-]+$"          @addedIn("2026-04-24T00:00:00Z")
	featureName:      string                                  @addedIn("2026-04-24T00:00:00Z")
	featDir:          string                                  @addedIn("2026-04-24T00:00:00Z")
	originalRequest:  *"" | string                            @addedIn("2026-04-24T00:00:00Z")
	operator:         #OperatorInfo                           @addedIn("2026-04-24T00:00:00Z")
	config:           #WorkflowConfig                         @addedIn("2026-04-24T00:00:00Z")
	startedAt:        time.Format(time.RFC3339)               @addedIn("2026-04-24T00:00:00Z")
	updatedAt:        time.Format(time.RFC3339)               @addedIn("2026-04-24T00:00:00Z")
	completedAt:      *null | time.Format(time.RFC3339)       @addedIn("2026-05-04T00:00:00Z")
	totalElapsedMin:  *0 | (float & >=0) | (int & >=0)        @addedIn("2026-05-04T00:00:00Z")
	currentStepId:    *"" | =~"^STEP_[0-9]{2}_[A-Z0-9_]+$"       @addedIn("2026-04-24T00:00:00Z")
	nextStepId:       *"" | =~"^STEP_[0-9]{2}_[A-Z0-9_]+$"       @addedIn("2026-04-24T00:00:00Z")
	totalSteps:       *0 | (int & >=0)                         @addedIn("2026-04-24T00:00:00Z")
	completedSteps:   *0 | (int & >=0)                         @addedIn("2026-04-24T00:00:00Z")
	notes:            *[] | [...#Note]                        @addedIn("2026-04-24T00:00:00Z")
	globalWarnings:   *[] | [...#GlobalWarning]               @addedIn("2026-04-24T00:00:00Z")
	steps:            [...#Step]                              @addedIn("2026-04-24T00:00:00Z")
}

#OperatorInfo: {
	locale: *"" | string @addedIn("2026-04-24T00:00:00Z")
}

#WorkflowConfig: {
	mode:                   *"autonomous" | "review" @addedIn("2026-04-24T00:00:00Z")
	executionStrategy?:     "serial" | "parallel-worktrees" | "agent-teams" | "parallel" @addedIn("2026-04-24T00:00:00Z")
	testExecutionDepth?:    "static-only" | "scoped-execute" | "full-rehearse" @addedIn("2026-04-30T18:00:00Z")
	testExecutionDepthAuto?: bool                                  @addedIn("2026-04-30T18:00:00Z")
	setAt:                  time.Format(time.RFC3339)              @addedIn("2026-04-24T00:00:00Z")
	switchedFrom?:          "autonomous" | "review"                @addedIn("2026-04-24T00:00:00Z")
	switchedAt?:            time.Format(time.RFC3339)              @addedIn("2026-04-24T00:00:00Z")
}

#Note: {
	at:      time.Format(time.RFC3339) @addedIn("2026-04-24T00:00:00Z")
	stepId?: =~"^STEP_[0-9]{2}_[A-Z0-9_]+$" @addedIn("2026-04-24T00:00:00Z")
	message: string                     @addedIn("2026-04-24T00:00:00Z")
}

#GlobalWarning: {
	at:      time.Format(time.RFC3339) @addedIn("2026-04-24T00:00:00Z")
	stepId?: =~"^STEP_[0-9]{2}_[A-Z0-9_]+$" @addedIn("2026-04-24T00:00:00Z")
	message: string                     @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// Step base + step-type discriminator
// =============================================================
// #Step is a discriminated union: name selects which payload struct applies.
// Closed disjunction — any name not in the registry is rejected.
#Step:
	#PRDStep |
	#TasksManifestStep |
	#TaskStep |
	#BrainstormingStep |
	#CodeReviewStep |
	#ReceivingCodeReviewStep |
	#WriteTestsStep |
	#UpdateDocsStep |
	#FeatureAcceptanceStep |
	#CommitStep

#StepName: "PRD" | "TASKS_MANIFEST" | "TASK" | "BRAINSTORMING" |
	"CODE_REVIEW" | "RECEIVING_CODE_REVIEW" | "WRITE_TESTS" |
	"UPDATE_DOCS" | "FEATURE_ACCEPTANCE" | "COMMIT"

#StepStatus: "PENDING" | "RUNNING" | "AWAITING_REVIEW" | "COMPLETED" |
	"STOPPED" | "PAUSED_PENDING_OPERATOR" | "SKIPPED" | "FAILED"

#StepBase: {
	stepId:         =~"^STEP_[0-9]{2}_[A-Z0-9_]+$"             @addedIn("2026-04-24T00:00:00Z")
	name:           #StepName                                @addedIn("2026-04-24T00:00:00Z")
	taskId?:        =~"^TASK_[0-9]{2}$"                     @addedIn("2026-04-24T00:00:00Z")
	status:         #StepStatus                              @addedIn("2026-04-24T00:00:00Z")
	applicability:  #StepApplicability                       @addedIn("2026-04-24T00:00:00Z")
	startedAt:      time.Format(time.RFC3339)                @addedIn("2026-04-24T00:00:00Z")
	completedAt:    *null | time.Format(time.RFC3339)        @addedIn("2026-04-24T00:00:00Z")
	elapsedMin:     *0 | (float & >=0) | (int & >=0)         @addedIn("2026-05-04T00:00:00Z")
	retryCount:     *0 | (int & >=0)                          @addedIn("2026-04-24T00:00:00Z")
	skipReason?:    null | string                            @addedIn("2026-04-24T00:00:00Z")
	itDependsOn:    *[] | [...string]                        @addedIn("2026-04-24T00:00:00Z")
	nextStep:       *"" | string                             @addedIn("2026-04-24T00:00:00Z")
	skillsToInvoke: *[] | [...string]                        @addedIn("2026-04-24T00:00:00Z")
	skillsInvoked:  *[] | [...string]                        @addedIn("2026-04-24T00:00:00Z")
	owner:          *null | string                           @addedIn("2026-04-24T00:00:00Z")
	worktrees?:     #StepWorktrees                           @addedIn("2026-04-24T00:00:00Z")
	warnings:       *[] | [...#Warning]                      @addedIn("2026-04-24T00:00:00Z")
	reviewHistory:  *[] | [...#ReviewExchange]               @addedIn("2026-04-24T00:00:00Z")
	dispatches:     *[] | [...#DispatchRecord]               @addedIn("2026-05-04T00:00:00Z")
	// Open struct: each #StepDefinitions.<NAME> adds its own payload field.
	...
}

#StepApplicability: {
	applicable: bool             @addedIn("2026-04-24T00:00:00Z")
	reason:     *"" | string     @addedIn("2026-04-24T00:00:00Z")
}

#StepWorktrees: {
	used:      bool                          @addedIn("2026-04-24T00:00:00Z")
	worktrees: *[] | [...#WorktreeEntry]     @addedIn("2026-04-24T00:00:00Z")
}

#WorktreeEntry: {
	name:   string                                                       @addedIn("2026-04-24T00:00:00Z")
	status: "ACTIVE" | "MERGED" | "PR_OPENED" | "ABANDONED" | "CREATED"  @addedIn("2026-04-24T00:00:00Z")
}

#Warning: {
	kind:    string                     @addedIn("2026-04-24T00:00:00Z")
	message: string                     @addedIn("2026-04-24T00:00:00Z")
	at?:     time.Format(time.RFC3339) @addedIn("2026-04-24T00:00:00Z")
}

#ReviewExchange: {
	round:          int & >=1                                   @addedIn("2026-04-24T00:00:00Z")
	proposal:       string                                       @addedIn("2026-04-24T00:00:00Z")
	operatorAction: "approve" | "adjust" | "skip" | "stop"      @addedIn("2026-04-24T00:00:00Z")
	operatorNote:   *"" | string                                 @addedIn("2026-04-24T00:00:00Z")
	decidedAt:      time.Format(time.RFC3339)                    @addedIn("2026-04-24T00:00:00Z")
}

#DispatchRecord: {
	agentId:               string                              @addedIn("2026-04-24T00:00:00Z")
	dispatchPromptDigest:  =~"^sha256:[a-f0-9]{64}$"           @addedIn("2026-05-04T00:00:00Z")
	promptByteCount:       int & >=1                           @addedIn("2026-05-04T00:00:00Z")
	promptPath:            string                              @addedIn("2026-05-04T00:00:00Z")
	renderTemplateUsed:    *null | string                      @addedIn("2026-05-04T00:00:00Z")
	dispatchedAt:          time.Format(time.RFC3339)           @addedIn("2026-05-04T00:00:00Z")
	model:                 *null | "haiku" | "sonnet" | "opus" @addedIn("2026-04-24T00:00:00Z")
	status:                *"in_progress" | "completed" | "failed" | "skipped" @addedIn("2026-04-24T00:00:00Z")
	findingsAddressed:     *[] | [...string]                   @addedIn("2026-04-24T00:00:00Z")
	filesModified:         *[] | [...string]                   @addedIn("2026-04-24T00:00:00Z")
	filesCreated:          *[] | [...string]                   @addedIn("2026-04-24T00:00:00Z")
	filesDeleted:          *[] | [...string]                   @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// PRD step + payload
// =============================================================
#PRDStep: #StepBase & {
	name: "PRD"
	prd:  #PRD
}

#PRD: {
	title:                     string              @addedIn("2026-04-24T00:00:00Z")
	overview:                  *"" | string        @addedIn("2026-04-24T00:00:00Z")
	personas:                  *[] | [...#Persona] @addedIn("2026-04-24T00:00:00Z")
	objectives:                *[] | [...string]   @addedIn("2026-04-24T00:00:00Z")
	inScope:                   *[] | [...string]   @addedIn("2026-04-24T00:00:00Z")
	outOfScope:                *[] | [...string]   @addedIn("2026-04-24T00:00:00Z")
	deliverables:              *[] | [...string]   @addedIn("2026-04-24T00:00:00Z")
	functionalRequirements:    [...#FR]            @addedIn("2026-04-24T00:00:00Z")
	nonFunctionalRequirements: *[] | [...#NFR]     @addedIn("2026-04-24T00:00:00Z")
	successMetrics:            *[] | [...#SuccessMetric] @addedIn("2026-04-24T00:00:00Z")
	acceptanceCriteria:        [...#AC]            @addedIn("2026-04-24T00:00:00Z")
	risks:                     *[] | [...#Risk]    @addedIn("2026-04-24T00:00:00Z")
	dependencies?:             #PRDDependencies    @addedIn("2026-04-24T00:00:00Z")
	assumptions:               *[] | [...string]   @addedIn("2026-04-24T00:00:00Z")
	taskGranularity:           *"" | string        @addedIn("2026-04-24T00:00:00Z")
}

#PRDDependencies: {
	external?: [...string] @addedIn("2026-04-24T00:00:00Z")
	internal?: [...string] @addedIn("2026-04-24T00:00:00Z")
}

#Persona:       {id: =~"^P-[0-9]+$" @addedIn("2026-04-24T00:00:00Z"), description: string @addedIn("2026-04-24T00:00:00Z")}
#FR:            {id: =~"^FR-[0-9]+$" @addedIn("2026-04-24T00:00:00Z"), text?: string @addedIn("2026-04-24T00:00:00Z"), description?: string @addedIn("2026-04-24T00:00:00Z"), priority?: "must" | "should" | "could" @addedIn("2026-04-24T00:00:00Z")}
#NFR:           {id: =~"^NFR-[0-9]+$" @addedIn("2026-04-24T00:00:00Z"), text?: string @addedIn("2026-04-24T00:00:00Z"), description?: string @addedIn("2026-04-24T00:00:00Z"), category?: string @addedIn("2026-04-24T00:00:00Z"), target: string @addedIn("2026-04-24T00:00:00Z")}
#SuccessMetric: {id: =~"^M-[0-9]+$" @addedIn("2026-04-24T00:00:00Z"), metric: string @addedIn("2026-04-24T00:00:00Z"), target: string @addedIn("2026-04-24T00:00:00Z"), method: string @addedIn("2026-04-24T00:00:00Z")}
#AC:            {id: =~"^AC-[0-9]+$" @addedIn("2026-04-24T00:00:00Z"), text?: string @addedIn("2026-04-24T00:00:00Z"), description?: string @addedIn("2026-04-24T00:00:00Z"), bindsTo?: [...=~"^FR-[0-9]+$"] @addedIn("2026-04-24T00:00:00Z")}
#Risk:          {id: =~"^R-[0-9]+$" @addedIn("2026-04-24T00:00:00Z"), text?: string @addedIn("2026-04-24T00:00:00Z"), description?: string @addedIn("2026-04-24T00:00:00Z"), mitigation: string @addedIn("2026-04-24T00:00:00Z")}

// =============================================================
// TASKS_MANIFEST
// =============================================================
#TasksManifestStep: #StepBase & {
	name:          "TASKS_MANIFEST"
	tasksManifest: #TasksManifest
}

#TasksManifest: {
	totalTasks:      int & >=0                           @addedIn("2026-04-24T00:00:00Z")
	tasksOrder:      [...=~"^TASK_[0-9]{2}$"]           @addedIn("2026-04-24T00:00:00Z")
	dependencyGraph: [string]: [...=~"^TASK_[0-9]{2}$"]
	parallelizable:  *[] | [...[...=~"^TASK_[0-9]{2}$"]] @addedIn("2026-04-24T00:00:00Z")
	tasks?:          [...#TaskBrief]                     @addedIn("2026-04-24T00:00:00Z")
	// suppressedRedundantTasks records candidate TASKs the Reviewer pass dropped
	// because their scope is a strict subset of a canonical pipeline phase
	// (write-tests, update-docs, code-review, commit, feature-acceptance,
	// receiving-code-review). Captured for retro auditability — the canonical
	// phase still runs at the end of the pipeline, so duplicating it as a TASK
	// is wasted dispatch. Added 2026-05-07 (RETRO §14 + §17).
	suppressedRedundantTasks?: [...#SuppressedRedundantTask] @addedIn("2026-05-07T00:00:00Z")
	// granularityWarnings records the output of the tertiary granularity-review
	// pass in `generate-task` (R-15 / RETRO §15). Each entry flags a task that
	// is too small (recommend collapse) or too large (recommend split). The
	// operator sees these before `execute-task` runs so the plan can be
	// adjusted without a full re-decomposition cycle. Added 2026-05-07.
	granularityWarnings?: [...#GranularityWarning] @addedIn("2026-05-07T00:00:00Z")
}

// #SuppressedRedundantTask — one entry per candidate TASK dropped by the
// Reviewer-pass canonical-phase filter. `reason` MUST follow the form
// `duplicates-canonical-phase-<phase>` so retros can group by phase.
#SuppressedRedundantTask: {
	candidateTitle: string                                    @addedIn("2026-05-07T00:00:00Z")
	candidateScope: [...string]                               @addedIn("2026-05-07T00:00:00Z")
	reason:         =~"^duplicates-canonical-phase-(write-tests|update-docs|code-review|commit|feature-acceptance|receiving-code-review)$" @addedIn("2026-05-07T00:00:00Z")
	detectedBy?:    *"reviewer-pass" | "operational-audit-pass" @addedIn("2026-05-07T00:00:00Z")
}

// #GranularityWarning — one entry per task flagged by the tertiary
// granularity-review pass in `generate-task` (R-15 / RETRO §15).
// `verdict` is "collapse" when the task has <2 files in scope (recommend
// merging with a sibling), or "split" when it has >10 files (recommend
// decomposing further). `reason` is the free-form prose from the haiku
// granularity reviewer. Added 2026-05-07.
#GranularityWarning: {
	taskId:  =~"^TASK_[0-9]{2}$" @addedIn("2026-05-07T00:00:00Z")
	verdict: "collapse" | "split" @addedIn("2026-05-07T00:00:00Z")
	reason:  string               @addedIn("2026-05-07T00:00:00Z")
}

#TaskBrief: {
	taskId:         =~"^TASK_[0-9]{2}$"                  @addedIn("2026-04-24T00:00:00Z")
	title:          string                                @addedIn("2026-04-24T00:00:00Z")
	suggestedModel: *null | "haiku" | "sonnet" | "opus" @addedIn("2026-04-24T00:00:00Z")
	trivial:        *false | bool                         @addedIn("2026-04-24T00:00:00Z")
	skillsFound:    *[] | [...string]                    @addedIn("2026-04-24T00:00:00Z")
	scope:          *[] | [...string]                    @addedIn("2026-04-24T00:00:00Z")
	dependsOn?:     [...=~"^TASK_[0-9]{2}$"]             @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// TASK step + payload
// =============================================================
#TaskStep: #StepBase & {
	name:   "TASK"
	taskId: =~"^TASK_[0-9]{2}$"
	task:   #TaskExecution
}

#TaskExecution: {
	title?:             string                  @addedIn("2026-04-24T00:00:00Z")
	scope:              [...string]             @addedIn("2026-04-24T00:00:00Z")
	dependsOn?:         [...=~"^TASK_[0-9]{2}$"] @addedIn("2026-04-24T00:00:00Z")
	invariants:         *[] | [...#Invariant]    @addedIn("2026-04-24T00:00:00Z")
	acceptanceCriteria?: [...#TaskAC]            @addedIn("2026-04-24T00:00:00Z")
	suggestedModel:     *null | "haiku" | "sonnet" | "opus" @addedIn("2026-04-24T00:00:00Z")
	trivial:            *false | bool                       @addedIn("2026-04-24T00:00:00Z")
	explorer?:          #TaskExplorer                       @addedIn("2026-04-24T00:00:00Z")
	reviewer?:          #TaskReviewer                       @addedIn("2026-04-24T00:00:00Z")
	execution:          #TaskExecutionResult                @addedIn("2026-04-24T00:00:00Z")
}

#Invariant: {
	rule:    string @addedIn("2026-04-24T00:00:00Z")
	source?: string @addedIn("2026-04-24T00:00:00Z")
}

#TaskAC: {
	id:       =~"^T-AC-[0-9]+$" @addedIn("2026-04-24T00:00:00Z")
	bindsTo:  [...=~"^AC-[0-9]+$"] @addedIn("2026-04-24T00:00:00Z")
	description?: string           @addedIn("2026-04-24T00:00:00Z")
}

#TaskExplorer: {
	model:         *null | "haiku" | "sonnet" | "opus" @addedIn("2026-04-24T00:00:00Z")
	completedAt?:  time.Format(time.RFC3339)            @addedIn("2026-04-24T00:00:00Z")
	filesModified: *[] | [...string]                    @addedIn("2026-04-24T00:00:00Z")
	filesToRead:   *[] | [...string]                    @addedIn("2026-04-24T00:00:00Z")
	depsGraph?:    [string]: {imports?: [...string], importedBy?: [...string]}
	domains:       *[] | [...string]                    @addedIn("2026-04-24T00:00:00Z")
	skillsFound:   *[] | [...#SkillFound]               @addedIn("2026-04-24T00:00:00Z")
}

#SkillFound: {
	domain:    string                                                         @addedIn("2026-04-24T00:00:00Z")
	skill:     string                                                         @addedIn("2026-04-24T00:00:00Z")
	relevance: *"med" | "high" | "low"                                        @addedIn("2026-04-24T00:00:00Z")
}

#TaskReviewer: {
	model:            *null | "haiku" | "sonnet" | "opus" @addedIn("2026-04-24T00:00:00Z")
	completedAt?:     time.Format(time.RFC3339)            @addedIn("2026-04-24T00:00:00Z")
	// additionalContext: free-form prose (default "") OR a structured object
	// consumable by the `reapply-additional-context` mutator (apply.go:1011).
	// String form is preserved for back-compat and skill orchestrators that
	// emit prose summaries; object form is the canonical shape consumed by
	// the mutator to translate Reviewer corrections into `task.scope` updates.
	additionalContext: *"" | string | #AdditionalContextObj  @addedIn("2026-04-24T00:00:00Z")
	skipTestsReason:   *null | string                        @addedIn("2026-04-24T00:00:00Z")
	testSpecs:         *[] | [...#TestSpec]                  @addedIn("2026-04-24T00:00:00Z")
}

// #AdditionalContextObj is the structured shape consumed by the
// `browzer workflow reapply-additional-context` mutator (apply.go:1011).
// Each #FileChange entry rewrites task.scope per its `kind`:
//   - corrected: replaces `from` with `to`
//   - added:     appends `path` (or `to` as fallback) to scope
//   - dropped:   removes `path` (or `from` as fallback) from scope
// Schema added 2026-05-04 (WF-SYNC-2): mutator already implemented this
// shape since 2026-04-24; the schema previously declared `string` only,
// causing CUE rejection of the legitimate runtime contract.
#AdditionalContextObj: {
	changes: [...#FileChange] @addedIn("2026-05-04T00:00:00Z")
}

// #FileChange entries rewrite TASK fields per `kind`:
//   - corrected:     replaces `from` path with `to` path inside task.scope
//   - added:         appends `path` (or `to` as fallback) to task.scope
//   - dropped:       removes `path` (or `from` as fallback) from task.scope
//   - rename-domain: rewrites `from` → `to` inside task.explorer.domains AND
//                    propagates the rename to task.explorer.skillsFound[].domain.
//                    Closes RETRO PR 6 §5.6 — previously, fixing a typo in a
//                    domain string required out-of-band jq surgery on the
//                    /tmp/reviewer-tasks.json file before append-steps.
#FileChange: {
	kind:    "corrected" | "added" | "dropped" | "rename-domain" @addedIn("2026-05-04T00:00:00Z")
	from?:   string                                              @addedIn("2026-05-04T00:00:00Z")
	to?:     string                                              @addedIn("2026-05-04T00:00:00Z")
	path?:   string                                              @addedIn("2026-05-04T00:00:00Z")
	reason?: string                                              @addedIn("2026-05-04T00:00:00Z")
}

// #TestSpec — Reviewer-emitted green-test plan entry. RETRO PR 6 §2.3 (2026-05-06):
//   - `type` (literal "green") was renamed to `intent` (the semantic axis: which
//     stage of the red→green→refactor loop the spec satisfies). Adding an enum
//     for `intent` clarifies that other plan stages exist; for now only "green"
//     is emitted by `generate-task` — `red` and `chaos` are reserved for future
//     reviewer passes that emit failing-first or fault-injection specs.
//   - `scope` is a NEW required field naming the testing layer the spec belongs
//     to. Replaces the cargo-culted `type: "unit"|"integration"|"e2e"` LLMs kept
//     emitting because the universal testing convention overloads the word
//     `type`. `scope` resolves the collision and is mandatory for the
//     `write-tests` skill's per-runner dispatch logic.
// CONTRACT BREAK: no `type:"green"` back-compat alias — schema v2 was already a
// hard cutoff per WORKFLOW_SYNC_REDESIGN.md §5; consumers must move to `intent`
// + `scope`.
#TestSpec: {
	testId:         =~"^T-[0-9]+$"                         @addedIn("2026-04-24T00:00:00Z")
	file:           string                                  @addedIn("2026-04-24T00:00:00Z")
	intent:         "green" | "red" | "chaos"               @addedIn("2026-05-06T00:00:00Z")
	scope:          "unit" | "integration" | "e2e" | "chaos" @addedIn("2026-05-06T00:00:00Z")
	description:    string                                  @addedIn("2026-04-24T00:00:00Z")
	coverageTarget?: string                                 @addedIn("2026-04-24T00:00:00Z")
}

#TaskExecutionResult: {
	gates:            #TaskGates                       @addedIn("2026-04-24T00:00:00Z")
	files?:           #TaskFiles                       @addedIn("2026-04-24T00:00:00Z")
	filesModified?:   [...string]                      @addedIn("2026-04-24T00:00:00Z")
	filesCreated?:    [...string]                      @addedIn("2026-04-24T00:00:00Z")
	filesDeleted?:    [...string]                      @addedIn("2026-04-24T00:00:00Z")
	scopeAdjustments: *[] | [...#ScopeAdjustment]      @addedIn("2026-04-24T00:00:00Z")
	agents:           *[] | [...#TaskAgent]            @addedIn("2026-04-24T00:00:00Z")
	invariantsChecked: *[] | [...#InvariantCheck]      @addedIn("2026-04-24T00:00:00Z")
	fileEditsSummary?: [string]: {edits?: int, details?: [...string]}
	testsRan?:         #TestsRan                       @addedIn("2026-04-24T00:00:00Z")
	nextSteps:         *"" | string                    @addedIn("2026-04-24T00:00:00Z")
}

#TaskFiles: {
	created:  *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
	modified: *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
	deleted:  *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
}

#TaskGates: {
	baseline:    #GateRow                @addedIn("2026-04-24T00:00:00Z")
	postChange:  #GateRow                @addedIn("2026-04-24T00:00:00Z")
	regression:  *[] | [...#RegressionRow] @addedIn("2026-04-24T00:00:00Z")
}

#GateRow: {
	lint?:      "pass" | "fail" | "skip"
	typecheck?: string
	tests?:     string
	...
}

#RegressionRow: {
	file:   string                  @addedIn("2026-04-24T00:00:00Z")
	test:   string                  @addedIn("2026-04-24T00:00:00Z")
	result: "pass" | "fail" | "skip" @addedIn("2026-04-24T00:00:00Z")
}

#TaskAgent: {
	role:          string                                              @addedIn("2026-04-24T00:00:00Z")
	skill:         string                                              @addedIn("2026-04-24T00:00:00Z")
	model:         *null | "haiku" | "sonnet" | "opus"                 @addedIn("2026-04-24T00:00:00Z")
	status:        "pending" | "running" | "completed" | "failed"      @addedIn("2026-04-24T00:00:00Z")
	startedAt?:    time.Format(time.RFC3339)                            @addedIn("2026-04-24T00:00:00Z")
	completedAt?:  time.Format(time.RFC3339)                            @addedIn("2026-04-24T00:00:00Z")
	skillsLoaded?: [...string]                                          @addedIn("2026-04-24T00:00:00Z")
	// First-class per-agent file ledger (TE2-T3.2, 2026-05-08). Closes
	// the cargo-culted `files=<created>/<modified>` cursor convention by
	// surfacing structured paths the orchestrator + code-review skill can
	// read without re-discovering via diff. Mirrors the pre-existing
	// shape on #DispatchRecord. Optional + default `[]` so existing
	// workflow.json fixtures stay valid under v2.
	filesCreated:  *[] | [...string]                                    @addedIn("2026-05-08T00:00:00Z")
	filesModified: *[] | [...string]                                    @addedIn("2026-05-08T00:00:00Z")
	notes?:        string                                               @addedIn("2026-04-24T00:00:00Z")
}

#InvariantCheck: {
	rule:    string                          @addedIn("2026-04-24T00:00:00Z")
	source:  string                          @addedIn("2026-04-24T00:00:00Z")
	status:  "passed" | "failed"             @addedIn("2026-04-24T00:00:00Z")
	note?:   string                          @addedIn("2026-04-24T00:00:00Z")
}

#TestsRan: {
	preChange?:  {testCount?: string, duration?: string, details?: string}
	postChange?: {testCount?: string, duration?: string, details?: string}
}

#ScopeAdjustment: {
	kind:           "spec-relaxation" | "scope-expansion" | "scope-reduction" |
		"no-op-refactor" | "out-of-scope-fix" | "deferred-to-followup" @addedIn("2026-04-30T18:00:00Z")
	adjustment:     string                                              @addedIn("2026-04-24T00:00:00Z")
	reason:         string                                              @addedIn("2026-04-24T00:00:00Z")
	resolution:     =~"^(accepted|rejected|deferred)( — .+)?$"          @addedIn("2026-04-24T00:00:00Z")
	owner:          "specialist" | "orchestrator"                       @addedIn("2026-04-24T00:00:00Z")
	loggedAsWarning?: bool                                              @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// BRAINSTORMING
// =============================================================
#BrainstormingStep: #StepBase & {
	name:          "BRAINSTORMING"
	brainstorming: #Brainstorming
}

#Brainstorming: {
	questionsAsked:   int & >=0                           @addedIn("2026-04-24T00:00:00Z")
	researchRoundRun: bool                                @addedIn("2026-04-24T00:00:00Z")
	researchAgents:   *0 | (int & >=0)                    @addedIn("2026-04-24T00:00:00Z")
	dimensions:       #BrainstormDimensions               @addedIn("2026-04-24T00:00:00Z")
	researchFindings: *[] | [...#ResearchFinding]         @addedIn("2026-04-24T00:00:00Z")
	assumptions:      *[] | [...string]                   @addedIn("2026-04-24T00:00:00Z")
	openRisks:        *[] | [...string]                   @addedIn("2026-04-24T00:00:00Z")
	decision:         *null | #BrainstormDecision         @addedIn("2026-05-05T00:00:00Z")
}

#BrainstormDecision: {
	chosen:                 string         @addedIn("2026-05-05T00:00:00Z")
	rationale:              string         @addedIn("2026-05-05T00:00:00Z")
	alternativesConsidered: *[] | [...#BrainstormAlternative] @addedIn("2026-05-05T00:00:00Z")
}

#BrainstormAlternative: {
	name:           string  @addedIn("2026-05-05T00:00:00Z")
	"reason-rejected": string @addedIn("2026-05-05T00:00:00Z")
}

#BrainstormDimensions: {
	primaryUser:        string         @addedIn("2026-04-24T00:00:00Z")
	jobToBeDone:        string         @addedIn("2026-04-24T00:00:00Z")
	successSignal:      string         @addedIn("2026-04-24T00:00:00Z")
	inScope:            [...string]    @addedIn("2026-04-24T00:00:00Z")
	outOfScope:         [...string]    @addedIn("2026-04-24T00:00:00Z")
	repoSurface:        *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
	techConstraints:    *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
	failureModes:       *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
	acceptanceCriteria: *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
	dependencies:       *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
	openQuestions:      *[] | [...string] @addedIn("2026-04-24T00:00:00Z")
}

#ResearchFinding: {
	question:   string                       @addedIn("2026-04-24T00:00:00Z")
	answer:     string                       @addedIn("2026-04-24T00:00:00Z")
	confidence: "high" | "med" | "low"       @addedIn("2026-04-24T00:00:00Z")
	sources:    *[] | [...string]            @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// CODE_REVIEW
// =============================================================
#CodeReviewStep: #StepBase & {
	name:       "CODE_REVIEW"
	codeReview: #CodeReview
}

#CodeReview: {
	agentTeamsEnabled?:  bool                                               @addedIn("2026-04-24T00:00:00Z")
	dispatchMode:        "agent-teams" | "parallel-with-consolidator"       @addedIn("2026-04-24T00:00:00Z")
	reviewTier:          "basic" | "recommended" | "custom"                 @addedIn("2026-04-24T00:00:00Z")
	tokenCostEstimate?:  int & >=0                                          @addedIn("2026-04-24T00:00:00Z")
	mandatoryMembers:    [...string]                                        @addedIn("2026-04-24T00:00:00Z")
	recommendedMembers:  *[] | [...string]                                  @addedIn("2026-04-24T00:00:00Z")
	customMembers:       *[] | [...string]                                  @addedIn("2026-04-24T00:00:00Z")
	consolidator?:       #CodeReviewConsolidator                            @addedIn("2026-04-24T00:00:00Z")
	baseline?:           #CodeReviewBaseline                                @addedIn("2026-04-24T00:00:00Z")
	severityCounts?:     #SeverityCounts                                    @addedIn("2026-04-24T00:00:00Z")
	cyclomaticAudit?:    #CyclomaticAudit                                   @addedIn("2026-04-24T00:00:00Z")
	duplicationFindings: *[] | [...#DuplicationFinding]                     @addedIn("2026-04-24T00:00:00Z")
	regressionRun:       *null | #RegressionRun                             @addedIn("2026-04-24T00:00:00Z")
	findings:            *[] | [...#Finding]                                @addedIn("2026-04-24T00:00:00Z")
	// True when the operator pre-registered dispatchMode + reviewTier in
	// `Skill(code-review, "dispatchMode: <…>; tier: <…>")` under autonomous
	// mode, bypassing the Phase-1 prompt. See code-review/SKILL.md §1.
	preRegistered:       *false | bool                                      @addedIn("2026-05-04T00:00:00Z")
	// Optional gate verdict for the consolidated review. When set, drives
	// automated merge/block logic in receiving-code-review skill.
	// "advisory-only" never blocks; "fail-on-high" blocks on severity=high;
	// "fail-on-medium-or-high" blocks on medium or high.
	gate?:               *null | "fail-on-high" | "fail-on-medium-or-high" | "advisory-only" @addedIn("2026-05-11T00:00:00Z")
	// Exit code of the consolidated gate check. null = not yet run.
	exitCode?:           *null | int                                        @addedIn("2026-05-11T00:00:00Z")
	// Sensitive-path scan result from the FR-3 gate (populated by code-review
	// skill when it detects files matching references/sensitive-paths.md).
	sensitivePathGate?:  *null | #SensitivePathGate                        @addedIn("2026-05-11T00:00:00Z")
}

#SensitivePathGate: {
	matched:      bool        @addedIn("2026-05-11T00:00:00Z")
	matchedFiles: [...string] @addedIn("2026-05-11T00:00:00Z")
}

#CodeReviewConsolidator: {
	mode:    "in-line" | "dispatched-agent" @addedIn("2026-04-24T00:00:00Z")
	reason?: string                          @addedIn("2026-04-24T00:00:00Z")
}

#CodeReviewBaseline: {
	source:      "workflow-json" | "fresh-run" | "hybrid" @addedIn("2026-04-24T00:00:00Z")
	reusedGates: *[] | [...string]                        @addedIn("2026-04-24T00:00:00Z")
	freshGates:  *[] | [...string]                        @addedIn("2026-04-24T00:00:00Z")
	duration?:   string                                    @addedIn("2026-04-24T00:00:00Z")
	// The literal lint+typecheck+test command run for the baseline gate,
	// persisted verbatim for the audit log. See code-review/SKILL.md §1.
	command:     *"" | string                              @addedIn("2026-05-04T00:00:00Z")
	// Enumerated per-test failures parsed from the baseline run.
	// Lumped counts (`failureCount: N` without an array) are rejected by
	// the skill's failure-enumeration contract — see code-review/SKILL.md
	// §1 + references/regression-tester.md §Pre-existing-on-main.
	failures:    *[] | [...#RegressionFailure]             @addedIn("2026-05-04T00:00:00Z")
}

#SeverityCounts: {
	high:   *0 | (int & >=0) @addedIn("2026-04-24T00:00:00Z")
	medium: *0 | (int & >=0) @addedIn("2026-04-24T00:00:00Z")
	low:    *0 | (int & >=0) @addedIn("2026-04-24T00:00:00Z")
}

#CyclomaticAudit: {
	conductedBy: string                          @addedIn("2026-04-24T00:00:00Z")
	files:       *[] | [...#CyclomaticFile]      @addedIn("2026-04-24T00:00:00Z")
}

#CyclomaticFile: {
	file:           string                              @addedIn("2026-04-24T00:00:00Z")
	maxComplexity:  int & >=0                           @addedIn("2026-04-24T00:00:00Z")
	threshold:      int & >=0                           @addedIn("2026-04-24T00:00:00Z")
	verdict:        "warn" | "ok" | "fail"              @addedIn("2026-04-24T00:00:00Z")
}

#DuplicationFinding: {
	pattern:             string         @addedIn("2026-04-24T00:00:00Z")
	files:               [...string]    @addedIn("2026-04-24T00:00:00Z")
	suggestedExtraction: string         @addedIn("2026-04-24T00:00:00Z")
}

#RegressionRun: {
	tool:           "vitest" | "pytest" | "go test" | "cargo test" | "jest" | "skipped" | "lefthook" @addedIn("2026-04-24T00:00:00Z")
	scope:          *"blast-radius" | string                                                          @addedIn("2026-04-24T00:00:00Z")
	command:        string                                                                            @addedIn("2026-04-24T00:00:00Z")
	commandSource:  "lefthook" | "husky" | "package-scripts" | "stack-default" | "operator"           @addedIn("2026-04-30T18:00:00Z")
	executionDepth: "static-only" | "scoped-execute" | "full-rehearse"                                @addedIn("2026-04-30T18:00:00Z")
	filesInRadius:  *0 | (int & >=0)                                                                  @addedIn("2026-04-24T00:00:00Z")
	testFilesExecuted: *0 | (int & >=0)                                                               @addedIn("2026-04-24T00:00:00Z")
	exitCode:       int                                                                                @addedIn("2026-04-24T00:00:00Z")
	passed:         int & >=0                                                                          @addedIn("2026-04-24T00:00:00Z")
	failed:         int & >=0                                                                          @addedIn("2026-04-24T00:00:00Z")
	skippedTests:   *0 | (int & >=0)                                                                  @addedIn("2026-04-24T00:00:00Z")
	duration?:      string                                                                             @addedIn("2026-04-24T00:00:00Z")
	skipped:        bool                                                                                @addedIn("2026-04-24T00:00:00Z")
	skipReason:     *null | string                                                                     @addedIn("2026-04-24T00:00:00Z")
	summary:        *"" | string                                                                       @addedIn("2026-04-24T00:00:00Z")
	failures:       *[] | [...#RegressionFailure]                                                      @addedIn("2026-04-24T00:00:00Z")
}

#RegressionFailure: {
	testFile: string @addedIn("2026-04-24T00:00:00Z")
	testName: string @addedIn("2026-04-24T00:00:00Z")
	error:    string @addedIn("2026-04-24T00:00:00Z")
}

#Finding: {
	id:            =~"^F-[0-9]+$"                            @addedIn("2026-04-24T00:00:00Z")
	domain:        string                                     @addedIn("2026-04-24T00:00:00Z")
	severity:      "high" | "medium" | "low"                  @addedIn("2026-04-24T00:00:00Z")
	category:      string                                     @addedIn("2026-04-24T00:00:00Z")
	file:          string                                     @addedIn("2026-04-24T00:00:00Z")
	line?:         int & >=1                                  @addedIn("2026-04-24T00:00:00Z")
	description:   string                                     @addedIn("2026-04-24T00:00:00Z")
	suggestedFix:  *"" | string                               @addedIn("2026-04-24T00:00:00Z")
	assignedSkill: *null | string                              @addedIn("2026-04-24T00:00:00Z")
	status:        "open" | "fixing" | "fixed" | "wontfix"    @addedIn("2026-04-24T00:00:00Z")
	// Set to true when the same finding was raised by an off-lane reviewer
	// (cross-domain duplicate). The off-lane copies become advisory and the
	// owning lane drives the fix. See code-review/references/severity-matrix.md §49.
	crossLaneOverlap: *false | bool                          @addedIn("2026-05-04T00:00:00Z")
	// IDs of sibling findings that were merged into this one during
	// consolidation (format: "<lane>-<seq>", e.g. "SR-3", "QA-1").
	mergedFrom?:      [...=~"^[A-Z]{2,6}-[0-9]+$"]           @addedIn("2026-05-11T00:00:00Z")
}

// =============================================================
// RECEIVING_CODE_REVIEW
// =============================================================
#ReceivingCodeReviewStep: #StepBase & {
	name:                "RECEIVING_CODE_REVIEW"
	receivingCodeReview: #ReceivingCodeReview
}

#ReceivingCodeReview: {
	iteration:   int & >=1                          @addedIn("2026-04-24T00:00:00Z")
	summary:     #ReceivingSummary                  @addedIn("2026-04-24T00:00:00Z")
	dispatches:  *[] | [...#ReceivingDispatch]      @addedIn("2026-04-24T00:00:00Z")
	unrecovered: *[] | [...#UnrecoveredFinding]     @addedIn("2026-04-24T00:00:00Z")
	notes:       *"" | string                        @addedIn("2026-04-24T00:00:00Z")
}

#ReceivingSummary: {
	total:       int & >=0 @addedIn("2026-04-24T00:00:00Z")
	fixed:       int & >=0 @addedIn("2026-04-24T00:00:00Z")
	unrecovered: int & >=0 @addedIn("2026-04-24T00:00:00Z")
}

#ReceivingDispatch: {
	findingId:        =~"^F-[0-9]+$"                                   @addedIn("2026-04-24T00:00:00Z")
	iteration:        int & >=1                                         @addedIn("2026-04-24T00:00:00Z")
	reason:           "initial" | "retry" | "research-then-sonnet" |
		"research-then-opus" | "staging-regression" | "post-deploy" |
		"operator-feedback"                                              @addedIn("2026-04-24T00:00:00Z")
	role:             string                                            @addedIn("2026-04-24T00:00:00Z")
	skill:            string                                            @addedIn("2026-04-24T00:00:00Z")
	model:            "sonnet" | "opus"                                 @addedIn("2026-04-24T00:00:00Z")
	status:           "fixed" | "failed" | "skipped"                    @addedIn("2026-04-24T00:00:00Z")
	filesChanged:     *[] | [...string]                                 @addedIn("2026-04-24T00:00:00Z")
	gatesPostFix?:    {lint?: string, typecheck?: string, tests?: string}
	researchBundle:   *null | string                                    @addedIn("2026-04-24T00:00:00Z")
	failureTrace:     *null | string                                    @addedIn("2026-04-24T00:00:00Z")
	startedAt:        time.Format(time.RFC3339)                          @addedIn("2026-04-24T00:00:00Z")
	completedAt:      time.Format(time.RFC3339)                          @addedIn("2026-04-24T00:00:00Z")
}

#UnrecoveredFinding: {
	findingId:        =~"^F-[0-9]+$"     @addedIn("2026-04-24T00:00:00Z")
	severity:         "high" | "medium" | "low" @addedIn("2026-04-24T00:00:00Z")
	lastTrace:        string                @addedIn("2026-04-24T00:00:00Z")
	totalIterations:  int & >=1             @addedIn("2026-04-24T00:00:00Z")
	modelsTried:      [...string]           @addedIn("2026-04-24T00:00:00Z")
	researchPassesRun: int & >=0            @addedIn("2026-04-24T00:00:00Z")
	loggedToTechDebt: *null | string        @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// WRITE_TESTS
// =============================================================
#WriteTestsStep: #StepBase & {
	name:       "WRITE_TESTS"
	writeTests: #WriteTests
}

#WriteTests: {
	skipped:         bool                                                          @addedIn("2026-04-24T00:00:00Z")
	skipReason:      *null | "no-test-setup" | string                              @addedIn("2026-04-24T00:00:00Z")
	runner:          *null | "vitest" | "jest" | "pytest" | "go test" | "cargo test" @addedIn("2026-04-24T00:00:00Z")
	filesAuthored:   *[] | [...string]                                             @addedIn("2026-04-24T00:00:00Z")
	greenTests?:     #GreenTests                                                   @addedIn("2026-04-24T00:00:00Z")
	mutationTesting?: #MutationTesting                                             @addedIn("2026-04-24T00:00:00Z")
	notes:           *"" | string                                                  @addedIn("2026-04-24T00:00:00Z")
	// Flat top-level aliases for mutation results (additive, backward-compat).
	// Both the nested mutationTesting.{score,…} and these flat fields are
	// accepted; tools may populate either or both. Added 2026-05-11.
	mutationScore?: *null | int                                                    @addedIn("2026-05-11T00:00:00Z")
	killed?:        *null | int                                                    @addedIn("2026-05-11T00:00:00Z")
	survived?:      *null | int                                                    @addedIn("2026-05-11T00:00:00Z")
	categories?:    *null | [...string]                                            @addedIn("2026-05-11T00:00:00Z")
}

#GreenTests: {
	added:     int & >=0 @addedIn("2026-04-24T00:00:00Z")
	augmented: *0 | (int & >=0) @addedIn("2026-04-24T00:00:00Z")
	duration?: string @addedIn("2026-04-24T00:00:00Z")
}

#MutationTesting: {
	ran:         bool                                       @addedIn("2026-04-24T00:00:00Z")
	tool:        *null | "stryker" | "mutmut" | "go-mutesting" @addedIn("2026-04-24T00:00:00Z")
	score:       *0 | (int & >=0 & <=100)                   @addedIn("2026-04-24T00:00:00Z")
	target:      *0 | (int & >=0 & <=100)                   @addedIn("2026-04-24T00:00:00Z")
	survivors:   *[] | [...#MutationSurvivor]               @addedIn("2026-04-24T00:00:00Z")
	coverageGap: *null | #MutationCoverageGap               @addedIn("2026-04-24T00:00:00Z")
}

#MutationSurvivor: {
	file:            string @addedIn("2026-04-24T00:00:00Z")
	line:            int & >=1 @addedIn("2026-04-24T00:00:00Z")
	mutator:         string @addedIn("2026-04-24T00:00:00Z")
	killedByNewTest: bool   @addedIn("2026-04-24T00:00:00Z")
	addedTestFile:   *"" | string @addedIn("2026-04-24T00:00:00Z")
}

#MutationCoverageGap: {
	reason:         string         @addedIn("2026-04-24T00:00:00Z")
	uncoveredFiles: [...string]    @addedIn("2026-04-24T00:00:00Z")
	remediation:    string         @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// UPDATE_DOCS
// =============================================================
#UpdateDocsStep: #StepBase & {
	name:       "UPDATE_DOCS"
	updateDocs: #UpdateDocs
}

#UpdateDocs: {
	docsMentioning:           *[] | [...#DocMention]                       @addedIn("2026-04-24T00:00:00Z")
	anchorDocsAlwaysIncluded: *[] | [...#AnchorDoc]                        @addedIn("2026-04-24T00:00:00Z")
	patches:                  *[] | [...#DocPatch]                         @addedIn("2026-04-24T00:00:00Z")
	twoPassRun:               #TwoPassRun                                  @addedIn("2026-04-24T00:00:00Z")
	// signals records the browzer-mentions / browzer-deps scan signals that
	// drove Phase A discovery. Each entry names the signal, its source command,
	// whether it returned results (hit), and an optional result count.
	signals?:    *null | [...#UpdateDocsSignal]                            @addedIn("2026-05-11T00:00:00Z")
	// enoentScan records whether Phase B ran a missing-file sweep and which
	// files (if any) were absent from the filesystem at patch time.
	enoentScan?: *null | #UpdateDocsEnoentScan                             @addedIn("2026-05-11T00:00:00Z")
}

#UpdateDocsSignal: {
	name:   string @addedIn("2026-05-11T00:00:00Z")
	source: string @addedIn("2026-05-11T00:00:00Z")
	hit:    bool   @addedIn("2026-05-11T00:00:00Z")
	count?: int    @addedIn("2026-05-11T00:00:00Z")
}

#UpdateDocsEnoentScan: {
	ran:          bool        @addedIn("2026-05-11T00:00:00Z")
	missingFiles: [...string] @addedIn("2026-05-11T00:00:00Z")
}

#DocMention: {
	sourceFile:  string                              @addedIn("2026-04-24T00:00:00Z")
	mentionedBy: [...{doc: string, confidence: float}]  @addedIn("2026-04-24T00:00:00Z")
}

#AnchorDoc: {
	doc:         string                                                                          @addedIn("2026-04-24T00:00:00Z")
	source:      "repo-root-changelog" | "walk-up" | "repo-root-debts" | "user-visible-change"   @addedIn("2026-04-24T00:00:00Z")
	disposition: "auto-included-fresh" | "deduped-vs-direct-ref" | "deduped-vs-mentions" |
		"deduped-vs-concept" | "skipped-no-user-visible-change" |
		"skipped-historical-archived"                                                             @addedIn("2026-04-24T00:00:00Z")
}

#DocPatch: {
	doc:          string                              @addedIn("2026-04-24T00:00:00Z")
	reason:       string                              @addedIn("2026-04-24T00:00:00Z")
	linesChanged: *0 | (int & >=0)                    @addedIn("2026-04-24T00:00:00Z")
	verdict:      "applied" | "skipped" | "failed"    @addedIn("2026-04-24T00:00:00Z")
	notes?:       string                              @addedIn("2026-04-24T00:00:00Z")
}

#TwoPassRun: {
	directRef:           bool                                                              @addedIn("2026-04-24T00:00:00Z")
	conceptLevel:        bool                                                              @addedIn("2026-04-24T00:00:00Z")
	mentionsPass:        bool                                                              @addedIn("2026-04-24T00:00:00Z")
	mentionsFallbackUsed: bool                                                             @addedIn("2026-04-30T18:00:00Z")
	mentionsResultEmpty:  *null | "all-new-files" | "no-edges" | "uncommitted-edits" |
		"index-lag"                                                                        @addedIn("2026-04-30T18:00:00Z")
	// REMOVED 2026-05-05 (B8): legacy `mentionsFallback: *null | string`. No active
	// consumer; superseded by `mentionsFallbackUsed` (boolean). Migration:
	//   jq 'del(.steps[].updateDocs.twoPassRun.mentionsFallback)' workflow.json
}

// =============================================================
// FEATURE_ACCEPTANCE
// =============================================================
#FeatureAcceptanceStep: #StepBase & {
	name:              "FEATURE_ACCEPTANCE"
	featureAcceptance: #FeatureAcceptance
}

#FeatureAcceptance: {
	// mode is now optional (not required) to allow partial/in-progress saves.
	// Enum extended to include "autonomous-with-stack-boot" (2026-05-11).
	mode?:                    "autonomous" | "manual" | "hybrid" | "autonomous-with-stack-boot" @addedIn("2026-04-24T00:00:00Z")
	modeNote?:                *"" | string                        @addedIn("2026-04-24T00:00:00Z")
	acceptanceCriteria:       [...#FAcceptanceCriterion]          @addedIn("2026-04-24T00:00:00Z")
	nfrVerifications:         *[] | [...#FNFR]                    @addedIn("2026-04-24T00:00:00Z")
	successMetrics:           *[] | [...#FSuccessMetric]          @addedIn("2026-04-24T00:00:00Z")
	acRelaxations:            *[] | [...#ACRelaxation]            @addedIn("2026-04-24T00:00:00Z")
	operatorActionsRequested: *[] | [...#OperatorAction]          @addedIn("2026-04-24T00:00:00Z")
	verdict?:                 "completed" | "stopped" | "paused-pending-operator" @addedIn("2026-05-04T00:00:00Z")
	executionRequiredProbe:   *false | bool                                       @addedIn("2026-05-04T00:00:00Z")
	liveVerificationAttempt:  *false | bool                                       @addedIn("2026-05-04T00:00:00Z")
	// True when the operator pre-registered `mode: <…>` in
	// `Skill(feature-acceptance, "mode: <autonomous|manual|hybrid>; …")`,
	// bypassing the Phase-1 prompt. See feature-acceptance/SKILL.md §1.
	preRegistered:            *false | bool                                       @addedIn("2026-05-04T00:00:00Z")
}

#FAcceptanceCriterion: {
	id:        =~"^AC-[0-9]+$"                          @addedIn("2026-04-24T00:00:00Z")
	status:    "verified" | "unverified" | "failed"     @addedIn("2026-04-24T00:00:00Z")
	evidence:  string                                    @addedIn("2026-04-24T00:00:00Z")
	method:    "test" | "inspect" | "metric"             @addedIn("2026-04-24T00:00:00Z")
	rationale?: string                                   @addedIn("2026-04-24T00:00:00Z")
}

#FNFR: {
	id:                        =~"^NFR-[0-9]+$"                  @addedIn("2026-04-24T00:00:00Z")
	status:                    "verified" | "partial" | "failed" @addedIn("2026-04-24T00:00:00Z")
	coversAcceptanceSignal:    "pass" | "warn" | "block"          @addedIn("2026-04-24T00:00:00Z")
	evidence:                  string                              @addedIn("2026-04-24T00:00:00Z")
	measured:                  string                              @addedIn("2026-04-24T00:00:00Z")
	target:                    string                              @addedIn("2026-04-24T00:00:00Z")
}

#FSuccessMetric: {
	id:       =~"^M-[0-9]+$"                  @addedIn("2026-04-24T00:00:00Z")
	measured: number | string                  @addedIn("2026-04-24T00:00:00Z")
	target:   number | string                  @addedIn("2026-04-24T00:00:00Z")
	status:   "met" | "unmet"                  @addedIn("2026-04-24T00:00:00Z")
	// Live-verification gate result. The §2.5 step in feature-acceptance
	// flips `resolved: true` only after the live probe agrees with `status`,
	// and records the `rationale` (reason / evidence pointer) so a later
	// audit can re-verify the decision. See feature-acceptance/references/
	// live-verify.md.
	resolved:  *false | bool                   @addedIn("2026-05-04T00:00:00Z")
	rationale: *"" | string                    @addedIn("2026-05-04T00:00:00Z")
}

#ACRelaxation: {
	acId:           =~"^AC-[0-9]+$"           @addedIn("2026-04-24T00:00:00Z")
	originalTarget: string                     @addedIn("2026-04-24T00:00:00Z")
	relaxedTarget:  string                     @addedIn("2026-04-24T00:00:00Z")
	rationale:      string                     @addedIn("2026-04-24T00:00:00Z")
	source:         "operator"                 @addedIn("2026-04-24T00:00:00Z")
	at:             time.Format(time.RFC3339) @addedIn("2026-04-24T00:00:00Z")
}

#OperatorAction: {
	ac:          *null | =~"^AC-[0-9]+$"                                                  @addedIn("2026-04-24T00:00:00Z")
	kind:        "deferred-post-merge" | "manual-verification" |
		"inherited-scope-adjustment" | "blocks-commit" | "deferred-follow-up" |
		"deferred-pre-commit"                                                              @addedIn("2026-04-24T00:00:00Z")
	description: string                                                                    @addedIn("2026-04-24T00:00:00Z")
	at:          time.Format(time.RFC3339)                                                 @addedIn("2026-04-24T00:00:00Z")
	resolved:    *false | bool                                                             @addedIn("2026-04-24T00:00:00Z")
	resolution:  *null | string                                                            @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// COMMIT
// =============================================================
#CommitStep: #StepBase & {
	name:   "COMMIT"
	commit: #CommitDescriptor
}

#CommitDescriptor: {
	sha?:              =~"^[a-f0-9]{7,40}$"                                       @addedIn("2026-04-24T00:00:00Z")
	backfillSha:       *null | =~"^[a-f0-9]{7,40}$"                               @addedIn("2026-05-07T00:00:00Z")
	conventionalType:  "feat" | "fix" | "chore" | "docs" | "refactor" | "test" |
		"perf" | "ci" | "build" | "style" | "revert"                              @addedIn("2026-04-24T00:00:00Z")
	scope:             *"" | string                                                @addedIn("2026-04-24T00:00:00Z")
	subject:           string                                                       @addedIn("2026-04-24T00:00:00Z")
	body:              *"" | string                                                 @addedIn("2026-04-24T00:00:00Z")
	trailers:          *[] | [...string]                                            @addedIn("2026-04-24T00:00:00Z")
	prePushAuditsRun:  *[] | [...string]                                            @addedIn("2026-04-24T00:00:00Z")
	pushAttempts:      *[] | [...#PushAttempt]                                      @addedIn("2026-04-24T00:00:00Z")
	prePushAudits:     *[] | [...#PrePushAudit]                                     @addedIn("2026-04-24T00:00:00Z")
}

#PushAttempt: {
	sha:              =~"^[a-f0-9]{7,40}$"  @addedIn("2026-04-24T00:00:00Z")
	attemptedAt:      time.Format(time.RFC3339) @addedIn("2026-04-24T00:00:00Z")
	lefthookBypassed: *false | bool         @addedIn("2026-04-24T00:00:00Z")
	noVerifyPassed:   *false | bool         @addedIn("2026-04-24T00:00:00Z")
	amendUsed:        *false | bool         @addedIn("2026-04-24T00:00:00Z")
	bypassedAudits:   *[] | [...string]     @addedIn("2026-04-24T00:00:00Z")
	bypassReason:     *null | string        @addedIn("2026-04-24T00:00:00Z")
	retryCount:       *0 | (int & >=0)      @addedIn("2026-04-24T00:00:00Z")
	previousFailure:  *null | string        @addedIn("2026-04-24T00:00:00Z")
}

#PrePushAudit: {
	name:     string                          @addedIn("2026-04-24T00:00:00Z")
	source:   "lefthook" | "husky" | "operator" @addedIn("2026-04-24T00:00:00Z")
	exitCode: int                              @addedIn("2026-04-24T00:00:00Z")
	durationMs: *0 | (int & >=0)               @addedIn("2026-04-24T00:00:00Z")
	output?:  string                           @addedIn("2026-04-24T00:00:00Z")
}

// =============================================================
// #StepView — JSON shape returned by `browzer get-step <PHASE> --json`.
// Consolidated, LLM-context-friendly projection of a single step.
// Only `stepId`, `phase`, and `status` are required; everything else is
// optional and depends on the phase.
// =============================================================
#StepView: {
	stepId:           =~"^STEP_[0-9]{2}_[A-Z0-9_]+$" @addedIn("2026-05-07T00:00:00Z")
	phase:            string                          @addedIn("2026-05-07T00:00:00Z")
	status:           #StepStatus                     @addedIn("2026-05-07T00:00:00Z")
	role?:            string                          @addedIn("2026-05-07T00:00:00Z")
	skillsToInvoke?:  [...string]                     @addedIn("2026-05-07T00:00:00Z")
	scope?:           {...}                           @addedIn("2026-05-07T00:00:00Z")
	prdContext?:      {...}                           @addedIn("2026-05-07T00:00:00Z")
	deps?:            {...}                           @addedIn("2026-05-07T00:00:00Z")
	invariants?:      [...string]                     @addedIn("2026-05-07T00:00:00Z")
	doneWhen?:        [...string]                     @addedIn("2026-05-07T00:00:00Z")
	body?:            {...}                           @addedIn("2026-05-07T00:00:00Z")
	tasks?: [...{...}]                                @addedIn("2026-05-07T00:00:00Z")
	originalRequest?:   string                        @addedIn("2026-05-07T00:00:00Z")
	brainstormSummary?: string                        @addedIn("2026-05-07T00:00:00Z")
}

// =============================================================
// Step-type discriminator registry — used by codegen + describe-step-type.
// =============================================================
#StepDefinitions: {
	PRD:                   #PRDStep
	TASKS_MANIFEST:        #TasksManifestStep
	TASK:                  #TaskStep
	BRAINSTORMING:         #BrainstormingStep
	CODE_REVIEW:           #CodeReviewStep
	RECEIVING_CODE_REVIEW: #ReceivingCodeReviewStep
	WRITE_TESTS:           #WriteTestsStep
	UPDATE_DOCS:           #UpdateDocsStep
	FEATURE_ACCEPTANCE:    #FeatureAcceptanceStep
	COMMIT:                #CommitStep
}
